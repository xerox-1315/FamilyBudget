package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"familybudget/internal/domain"
)

// Integration tests run only when TEST_DATABASE_URL points to a disposable database.
func openTest(t *testing.T) *Postgres {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.pool.Exec(ctx, `TRUNCATE expenses, users RESTART IDENTITY`); err != nil {
		t.Fatal(err)
	}
	return db
}

func date(s string) time.Time {
	d, _ := domain.ParseDate(s)
	return d
}

func TestPostgresExpenses(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	for _, id := range []int64{1, 2} {
		if err := db.UpsertUser(ctx, domain.User{TelegramID: id, FirstName: "u"}); err != nil {
			t.Fatal(err)
		}
	}

	created, err := db.CreateExpenses(ctx, []domain.NewExpense{
		{UserID: 1, Amount: 35000, Category: domain.CategoryFood, SpentAt: date("2026-10-16"), Comment: "Кофе"},
		{UserID: 1, Amount: 120050, Category: domain.CategoryTransport, SpentAt: date("2026-10-10"), Comment: "такси 50%_off"},
		{UserID: 2, Amount: 99900, Category: domain.CategoryFood, SpentAt: date("2026-10-12")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created[2].Comment != "" || created[1].Amount != 120050 || !created[0].SpentAt.Equal(date("2026-10-16")) {
		t.Fatalf("created = %+v", created)
	}

	from, to := date("2026-10-11"), date("2026-10-31")
	items, err := db.ListExpenses(ctx, domain.ExpenseFilter{From: &from, To: &to, Categories: []domain.Category{domain.CategoryFood}})
	if err != nil || len(items) != 2 || items[0].Comment != "Кофе" {
		t.Fatalf("date+category filter: %v %+v", err, items)
	}
	items, _ = db.ListExpenses(ctx, domain.ExpenseFilter{Query: "кофе"})
	if len(items) != 1 {
		t.Fatalf("case-insensitive search: %+v", items)
	}
	items, _ = db.ListExpenses(ctx, domain.ExpenseFilter{Query: "%"})
	if len(items) != 1 || items[0].ID != created[1].ID {
		t.Fatalf("LIKE wildcards must be escaped: %+v", items)
	}
	items, _ = db.ListExpenses(ctx, domain.ExpenseFilter{UserIDs: []int64{1}, Sort: domain.SortLastAdded, Limit: 1})
	if len(items) != 1 || items[0].ID != created[1].ID {
		t.Fatalf("last added: %+v", items)
	}

	sum, err := db.Summarize(ctx, domain.ExpenseFilter{UserIDs: []int64{1}})
	if err != nil || sum.Total != 155050 || sum.Count != 2 || len(sum.ByCategory) != 2 {
		t.Fatalf("summary: %v %+v", err, sum)
	}

	// Owner scoping: user 2 cannot change or delete user 1's expense.
	other := int64(2)
	amount := domain.Money(1)
	if _, err := db.UpdateExpense(ctx, created[0].ID, &other, domain.ExpensePatch{Amount: &amount}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign update: %v", err)
	}
	if deleted, err := db.DeleteExpenses(ctx, []int64{created[0].ID}, &other); err != nil || len(deleted) != 0 {
		t.Fatalf("foreign delete: %v %+v", err, deleted)
	}

	owner := int64(1)
	empty := ""
	upd, err := db.UpdateExpense(ctx, created[0].ID, &owner, domain.ExpensePatch{Amount: &amount, Comment: &empty})
	if err != nil || upd.Amount != 1 || upd.Comment != "" || upd.Category != domain.CategoryFood || !upd.UpdatedAt.After(upd.CreatedAt) {
		t.Fatalf("update: %v %+v", err, upd)
	}
	deleted, err := db.DeleteExpenses(ctx, []int64{created[0].ID, created[1].ID}, &owner)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("delete: %v %+v", err, deleted)
	}
	if _, err := db.GetExpense(ctx, created[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
}

func TestPostgresMembershipAndSettings(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	if _, err := db.pool.Exec(ctx, `DELETE FROM settings`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertUser(ctx, domain.User{TelegramID: 1, FirstName: "Аня"}); err != nil {
		t.Fatal(err)
	}
	u, err := db.GetUser(ctx, 1)
	if err != nil || u.IsMember || u.JoinedAt != nil {
		t.Fatalf("new user: %v %+v", err, u)
	}
	if err := db.SetMember(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	// A profile update must not touch membership.
	if err := db.UpsertUser(ctx, domain.User{TelegramID: 1, FirstName: "Анна"}); err != nil {
		t.Fatal(err)
	}
	u, _ = db.GetUser(ctx, 1)
	if !u.IsMember || u.JoinedAt == nil || u.FirstName != "Анна" {
		t.Fatalf("member: %+v", u)
	}
	if err := db.SetName(ctx, 1, "Мама"); err != nil {
		t.Fatal(err)
	}
	// The chosen name survives Telegram profile updates.
	_ = db.UpsertUser(ctx, domain.User{TelegramID: 1, FirstName: "Анна"})
	if u, _ = db.GetUser(ctx, 1); u.DisplayName() != "Мама" {
		t.Fatalf("name lost on profile update: %+v", u)
	}
	if err := db.SetName(ctx, 99, "X"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rename unknown user: %v", err)
	}
	if err := db.SetMember(ctx, 99, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	if v, _ := db.Setting(ctx, "k"); v != "" {
		t.Fatalf("missing setting = %q", v)
	}
	first, _ := db.SetSettingIfEmpty(ctx, "k", "a")
	second, _ := db.SetSettingIfEmpty(ctx, "k", "b")
	if first != "a" || second != "a" {
		t.Fatalf("SetSettingIfEmpty = %q, %q", first, second)
	}
	_ = db.SetSetting(ctx, "k", "c")
	if v, _ := db.Setting(ctx, "k"); v != "c" {
		t.Fatalf("setting = %q", v)
	}
}
