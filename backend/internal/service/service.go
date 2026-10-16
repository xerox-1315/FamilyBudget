// Package service holds the business logic shared by the REST API and the Telegram bot.
package service

import (
	"context"
	"fmt"

	"familybudget/internal/domain"
)

// Store is the persistence layer.
type Store interface {
	UpsertUser(ctx context.Context, u domain.User) error
	ListUsers(ctx context.Context) ([]domain.User, error)
	UserExists(ctx context.Context, id int64) (bool, error)
	GetUser(ctx context.Context, id int64) (domain.User, error)
	SetMember(ctx context.Context, id int64, member bool) error
	SetName(ctx context.Context, id int64, name string) error

	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	SetSettingIfEmpty(ctx context.Context, key, value string) (string, error)

	CreateExpenses(ctx context.Context, items []domain.NewExpense) ([]domain.Expense, error)
	GetExpense(ctx context.Context, id int64) (domain.Expense, error)
	ListExpenses(ctx context.Context, f domain.ExpenseFilter) ([]domain.Expense, error)
	Summarize(ctx context.Context, f domain.ExpenseFilter) (domain.Summary, error)
	UpdateExpense(ctx context.Context, id int64, owner *int64, patch domain.ExpensePatch) (domain.Expense, error)
	DeleteExpenses(ctx context.Context, ids []int64, owner *int64) ([]domain.Expense, error)
}

// MaxBatch limits how many expenses can be created at once.
const MaxBatch = 50

type Expenses struct {
	store Store
}

func New(store Store) *Expenses { return &Expenses{store: store} }

func (s *Expenses) TouchUser(ctx context.Context, u domain.User) error {
	return s.store.UpsertUser(ctx, u)
}

func (s *Expenses) Users(ctx context.Context) ([]domain.User, error) {
	return s.store.ListUsers(ctx)
}

func (s *Expenses) Create(ctx context.Context, items []domain.NewExpense) ([]domain.Expense, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("no expenses to add")
	}
	if len(items) > MaxBatch {
		return nil, domain.Invalid(fmt.Sprintf("too many expenses at once, max %d", MaxBatch))
	}
	checked := map[int64]bool{}
	for i := range items {
		if err := items[i].Normalize(); err != nil {
			return nil, err
		}
		uid := items[i].UserID
		if checked[uid] {
			continue
		}
		ok, err := s.store.UserExists(ctx, uid)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, domain.Invalid(fmt.Sprintf("unknown user %d", uid))
		}
		checked[uid] = true
	}
	return s.store.CreateExpenses(ctx, items)
}

func (s *Expenses) Get(ctx context.Context, id int64) (domain.Expense, error) {
	return s.store.GetExpense(ctx, id)
}

func (s *Expenses) List(ctx context.Context, f domain.ExpenseFilter) ([]domain.Expense, error) {
	if err := validateFilter(&f); err != nil {
		return nil, err
	}
	return s.store.ListExpenses(ctx, f)
}

func (s *Expenses) Summarize(ctx context.Context, f domain.ExpenseFilter) (domain.Summary, error) {
	if err := validateFilter(&f); err != nil {
		return domain.Summary{}, err
	}
	f.Limit, f.Offset = 0, 0
	return s.store.Summarize(ctx, f)
}

// Update changes an expense. A non-nil owner restricts the change to that user's expenses.
func (s *Expenses) Update(ctx context.Context, id int64, owner *int64, patch domain.ExpensePatch) (domain.Expense, error) {
	if patch.Empty() {
		return domain.Expense{}, domain.Invalid("nothing to update")
	}
	if err := patch.Normalize(); err != nil {
		return domain.Expense{}, err
	}
	return s.store.UpdateExpense(ctx, id, owner, patch)
}

// Delete removes expenses. A non-nil owner restricts the deletion to that user's expenses.
func (s *Expenses) Delete(ctx context.Context, ids []int64, owner *int64) ([]domain.Expense, error) {
	if len(ids) == 0 {
		return nil, domain.Invalid("no expenses to delete")
	}
	return s.store.DeleteExpenses(ctx, ids, owner)
}

func validateFilter(f *domain.ExpenseFilter) error {
	for _, c := range f.Categories {
		if !c.Valid() {
			return domain.Invalid("unknown category: " + string(c))
		}
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return domain.Invalid("date_from is after date_to")
	}
	if f.Limit < 0 || f.Offset < 0 {
		return domain.Invalid("limit and offset must not be negative")
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	return nil
}
