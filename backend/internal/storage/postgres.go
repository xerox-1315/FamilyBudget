// Package storage implements persistence on PostgreSQL.
package storage

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"familybudget/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Postgres struct {
	pool *pgxpool.Pool
}

// Open connects to the database, waiting for it to become available, and applies migrations.
func Open(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := waitReady(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func waitReady(ctx context.Context, pool *pgxpool.Pool) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := pool.Ping(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database is not reachable: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(migrations, "migrations"))
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// --- users ---

func (p *Postgres) UpsertUser(ctx context.Context, u domain.User) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO users (telegram_id, username, first_name, last_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (telegram_id) DO UPDATE
		SET username = EXCLUDED.username,
		    first_name = EXCLUDED.first_name,
		    last_name = EXCLUDED.last_name,
		    updated_at = now()
		WHERE (users.username, users.first_name, users.last_name)
		      IS DISTINCT FROM (EXCLUDED.username, EXCLUDED.first_name, EXCLUDED.last_name)`,
		u.TelegramID, u.Username, u.FirstName, u.LastName)
	return err
}

const userColumns = `telegram_id, username, first_name, last_name, display_name, is_member, joined_at`

func scanUser(r pgx.Row) (domain.User, error) {
	var u domain.User
	err := r.Scan(&u.TelegramID, &u.Username, &u.FirstName, &u.LastName, &u.Name, &u.IsMember, &u.JoinedAt)
	return u, err
}

// SetName stores the member's chosen name; an empty name falls back to the Telegram one.
func (p *Postgres) SetName(ctx context.Context, id int64, name string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE users SET display_name = $2, updated_at = now() WHERE telegram_id = $1`, id, name)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (p *Postgres) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT `+userColumns+` FROM users
		ORDER BY COALESCE(NULLIF(display_name, ''), first_name), last_name, telegram_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.User, error) { return scanUser(r) })
}

func (p *Postgres) GetUser(ctx context.Context, id int64) (domain.User, error) {
	u, err := scanUser(p.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE telegram_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return u, domain.ErrNotFound
	}
	return u, err
}

// SetMember changes family membership; joined_at is set each time a user joins.
func (p *Postgres) SetMember(ctx context.Context, id int64, member bool) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE users SET
			is_member = $2,
			joined_at = CASE WHEN $2 AND NOT is_member THEN now() ELSE joined_at END,
			updated_at = now()
		WHERE telegram_id = $1`, id, member)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// Setting returns a stored value, or "" if it is not set.
func (p *Postgres) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := p.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSettingIfEmpty stores the value unless one exists, and returns the stored value.
// It lets concurrent callers agree on one generated value.
func (p *Postgres) SetSettingIfEmpty(ctx context.Context, key, value string) (string, error) {
	var v string
	err := p.pool.QueryRow(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET key = EXCLUDED.key
		RETURNING value`, key, value).Scan(&v)
	return v, err
}

func (p *Postgres) SetSetting(ctx context.Context, key, value string) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

func (p *Postgres) UserExists(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE telegram_id = $1)`, id).Scan(&ok)
	return ok, err
}

// --- expenses ---

const expenseColumns = `id, user_id, amount, category, spent_at, COALESCE(comment, ''), created_at, updated_at`

func scanExpense(r pgx.Row) (domain.Expense, error) {
	var e domain.Expense
	var cat string
	err := r.Scan(&e.ID, &e.UserID, &e.Amount, &cat, &e.SpentAt, &e.Comment, &e.CreatedAt, &e.UpdatedAt)
	e.Category = domain.Category(cat)
	return e, err
}

func collectExpenses(rows pgx.Rows) ([]domain.Expense, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Expense, error) { return scanExpense(r) })
}

func (p *Postgres) CreateExpenses(ctx context.Context, items []domain.NewExpense) ([]domain.Expense, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := make([]domain.Expense, 0, len(items))
	for _, it := range items {
		e, err := scanExpense(tx.QueryRow(ctx, `
			INSERT INTO expenses (user_id, amount, category, spent_at, comment)
			VALUES ($1, $2, $3::text::expense_category, $4, NULLIF($5, ''))
			RETURNING `+expenseColumns,
			it.UserID, int64(it.Amount), string(it.Category), it.SpentAt, it.Comment))
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, tx.Commit(ctx)
}

func (p *Postgres) GetExpense(ctx context.Context, id int64) (domain.Expense, error) {
	e, err := scanExpense(p.pool.QueryRow(ctx, `SELECT `+expenseColumns+` FROM expenses WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return e, domain.ErrNotFound
	}
	return e, err
}

func (p *Postgres) ListExpenses(ctx context.Context, f domain.ExpenseFilter) ([]domain.Expense, error) {
	where, args := buildWhere(f)
	order := "spent_at DESC, id DESC"
	switch f.Sort {
	case domain.SortOldest:
		order = "spent_at ASC, id ASC"
	case domain.SortLastAdded:
		order = "id DESC"
	}
	q := `SELECT ` + expenseColumns + ` FROM expenses` + where + ` ORDER BY ` + order
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += ` LIMIT $` + strconv.Itoa(len(args))
	}
	if f.Offset > 0 {
		args = append(args, f.Offset)
		q += ` OFFSET $` + strconv.Itoa(len(args))
	}
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return collectExpenses(rows)
}

func (p *Postgres) Summarize(ctx context.Context, f domain.ExpenseFilter) (domain.Summary, error) {
	where, args := buildWhere(f)
	rows, err := p.pool.Query(ctx, `
		SELECT category::text, SUM(amount), COUNT(*) FROM expenses`+where+`
		GROUP BY category ORDER BY SUM(amount) DESC`, args...)
	if err != nil {
		return domain.Summary{}, err
	}
	totals, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.CategoryTotal, error) {
		var t domain.CategoryTotal
		var cat string
		err := r.Scan(&cat, &t.Amount, &t.Count)
		t.Category = domain.Category(cat)
		return t, err
	})
	if err != nil {
		return domain.Summary{}, err
	}
	s := domain.Summary{ByCategory: totals}
	for _, t := range totals {
		s.Total += t.Amount
		s.Count += t.Count
	}
	return s, nil
}

// UpdateExpense applies the patch. If owner is not nil, only that user's expense can be changed.
func (p *Postgres) UpdateExpense(ctx context.Context, id int64, owner *int64, patch domain.ExpensePatch) (domain.Expense, error) {
	var amount *int64
	if patch.Amount != nil {
		v := int64(*patch.Amount)
		amount = &v
	}
	var category *string
	if patch.Category != nil {
		v := string(*patch.Category)
		category = &v
	}
	e, err := scanExpense(p.pool.QueryRow(ctx, `
		UPDATE expenses SET
			amount     = COALESCE($2, amount),
			category   = COALESCE($3::text::expense_category, category),
			spent_at   = COALESCE($4, spent_at),
			comment    = CASE WHEN $5::boolean THEN NULLIF($6, '') ELSE comment END,
			updated_at = now()
		WHERE id = $1 AND ($7::bigint IS NULL OR user_id = $7)
		RETURNING `+expenseColumns,
		id, amount, category, patch.SpentAt, patch.Comment != nil, deref(patch.Comment), owner))
	if errors.Is(err, pgx.ErrNoRows) {
		return e, domain.ErrNotFound
	}
	return e, err
}

// DeleteExpenses removes expenses and returns the deleted ones. If owner is not nil,
// other users' expenses are left untouched.
func (p *Postgres) DeleteExpenses(ctx context.Context, ids []int64, owner *int64) ([]domain.Expense, error) {
	rows, err := p.pool.Query(ctx, `
		DELETE FROM expenses
		WHERE id = ANY($1) AND ($2::bigint IS NULL OR user_id = $2)
		RETURNING `+expenseColumns, ids, owner)
	if err != nil {
		return nil, err
	}
	return collectExpenses(rows)
}

func buildWhere(f domain.ExpenseFilter) (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		conds = append(conds, strings.ReplaceAll(cond, "$?", "$"+strconv.Itoa(len(args))))
	}
	if len(f.UserIDs) > 0 {
		add("user_id = ANY($?)", f.UserIDs)
	}
	if len(f.Categories) > 0 {
		cats := make([]string, len(f.Categories))
		for i, c := range f.Categories {
			cats[i] = string(c)
		}
		add("category::text = ANY($?)", cats)
	}
	if f.From != nil {
		add("spent_at >= $?", *f.From)
	}
	if f.To != nil {
		add("spent_at <= $?", *f.To)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add(`comment ILIKE '%' || $? || '%'`, escapeLike(q))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
