// Package memstore is an in-memory implementation of service.Store for tests.
package memstore

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"familybudget/internal/domain"
)

type Store struct {
	mu       sync.Mutex
	users    map[int64]domain.User
	expenses map[int64]domain.Expense
	settings map[string]string
	nextID   int64
	Now      func() time.Time
}

func New() *Store {
	return &Store{
		users:    map[int64]domain.User{},
		expenses: map[int64]domain.Expense{},
		settings: map[string]string{},
		Now:      time.Now,
	}
}

// UpsertUser stores profile fields; membership is changed only by SetMember.
func (s *Store) UpsertUser(_ context.Context, u domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.users[u.TelegramID]; ok {
		u.IsMember, u.JoinedAt, u.Name = old.IsMember, old.JoinedAt, old.Name
	} else {
		u.IsMember, u.JoinedAt, u.Name = false, nil, ""
	}
	s.users[u.TelegramID] = u
	return nil
}

func (s *Store) GetUser(_ context.Context, id int64) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return u, domain.ErrNotFound
	}
	return u, nil
}

func (s *Store) SetMember(_ context.Context, id int64, member bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	if member && !u.IsMember {
		now := s.Now()
		u.JoinedAt = &now
	}
	u.IsMember = member
	s.users[id] = u
	return nil
}

func (s *Store) SetName(_ context.Context, id int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	u.Name = name
	s.users[id] = u
	return nil
}

func (s *Store) Setting(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings[key], nil
}

func (s *Store) SetSettingIfEmpty(_ context.Context, key, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.settings[key]; ok {
		return v, nil
	}
	s.settings[key] = value
	return value, nil
}

func (s *Store) SetSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

func (s *Store) ListUsers(context.Context) ([]domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TelegramID < out[j].TelegramID })
	return out, nil
}

func (s *Store) UserExists(_ context.Context, id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.users[id]
	return ok, nil
}

func (s *Store) CreateExpenses(_ context.Context, items []domain.NewExpense) ([]domain.Expense, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	out := make([]domain.Expense, 0, len(items))
	for _, it := range items {
		s.nextID++
		e := domain.Expense{
			ID: s.nextID, UserID: it.UserID, Amount: it.Amount, Category: it.Category,
			SpentAt: it.SpentAt, Comment: it.Comment, CreatedAt: now, UpdatedAt: now,
		}
		s.expenses[e.ID] = e
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) GetExpense(_ context.Context, id int64) (domain.Expense, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.expenses[id]
	if !ok {
		return e, domain.ErrNotFound
	}
	return e, nil
}

func (s *Store) ListExpenses(_ context.Context, f domain.ExpenseFilter) ([]domain.Expense, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.filter(f)
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *Store) Summarize(_ context.Context, f domain.ExpenseFilter) (domain.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byCat := map[domain.Category]*domain.CategoryTotal{}
	var sum domain.Summary
	for _, e := range s.filter(f) {
		t, ok := byCat[e.Category]
		if !ok {
			t = &domain.CategoryTotal{Category: e.Category}
			byCat[e.Category] = t
		}
		t.Amount += e.Amount
		t.Count++
		sum.Total += e.Amount
		sum.Count++
	}
	for _, t := range byCat {
		sum.ByCategory = append(sum.ByCategory, *t)
	}
	sort.Slice(sum.ByCategory, func(i, j int) bool { return sum.ByCategory[i].Amount > sum.ByCategory[j].Amount })
	return sum, nil
}

func (s *Store) UpdateExpense(_ context.Context, id int64, owner *int64, p domain.ExpensePatch) (domain.Expense, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.expenses[id]
	if !ok || (owner != nil && e.UserID != *owner) {
		return domain.Expense{}, domain.ErrNotFound
	}
	if p.Amount != nil {
		e.Amount = *p.Amount
	}
	if p.Category != nil {
		e.Category = *p.Category
	}
	if p.SpentAt != nil {
		e.SpentAt = *p.SpentAt
	}
	if p.Comment != nil {
		e.Comment = *p.Comment
	}
	e.UpdatedAt = s.Now()
	s.expenses[id] = e
	return e, nil
}

func (s *Store) DeleteExpenses(_ context.Context, ids []int64, owner *int64) ([]domain.Expense, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Expense
	for _, id := range ids {
		e, ok := s.expenses[id]
		if !ok || (owner != nil && e.UserID != *owner) {
			continue
		}
		delete(s.expenses, id)
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) filter(f domain.ExpenseFilter) []domain.Expense {
	var out []domain.Expense
	q := strings.ToLower(strings.TrimSpace(f.Query))
	for _, e := range s.expenses {
		if len(f.UserIDs) > 0 && !slices.Contains(f.UserIDs, e.UserID) {
			continue
		}
		if len(f.Categories) > 0 && !slices.Contains(f.Categories, e.Category) {
			continue
		}
		if f.From != nil && e.SpentAt.Before(*f.From) {
			continue
		}
		if f.To != nil && e.SpentAt.After(*f.To) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Comment), q) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch f.Sort {
		case domain.SortLastAdded:
			return a.ID > b.ID
		case domain.SortOldest:
			if !a.SpentAt.Equal(b.SpentAt) {
				return a.SpentAt.Before(b.SpentAt)
			}
			return a.ID < b.ID
		}
		if !a.SpentAt.Equal(b.SpentAt) {
			return a.SpentAt.After(b.SpentAt)
		}
		return a.ID > b.ID
	})
	return out
}
