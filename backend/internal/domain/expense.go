// Package domain contains the core entities of the expense tracker.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrNotFound = errors.New("not found")

// ValidationError describes invalid input; transports turn it into a client error.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

func Invalid(msg string) error { return &ValidationError{Msg: msg} }

// DateLayout is the wire format of dates in the API and agent tools.
const DateLayout = "2006-01-02"

// MaxCommentLen limits the length of an expense comment.
const MaxCommentLen = 500

type Expense struct {
	ID        int64
	UserID    int64
	Amount    Money
	Category  Category
	SpentAt   time.Time // date only, midnight UTC
	Comment   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewExpense holds the fields needed to create an expense.
type NewExpense struct {
	UserID   int64
	Amount   Money
	Category Category
	SpentAt  time.Time
	Comment  string
}

func (e *NewExpense) Normalize() error {
	if e.UserID == 0 {
		return Invalid("user_id is required")
	}
	if e.Amount <= 0 {
		return Invalid("amount must be positive")
	}
	if e.Amount > MaxAmount {
		return Invalid("amount is too large")
	}
	if !e.Category.Valid() {
		return Invalid("unknown category: " + string(e.Category))
	}
	if e.SpentAt.IsZero() {
		return Invalid("spent_at is required")
	}
	e.SpentAt = DateOnly(e.SpentAt)
	c, err := normalizeComment(e.Comment)
	if err != nil {
		return err
	}
	e.Comment = c
	return nil
}

func normalizeComment(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", Invalid("comment must be valid UTF-8")
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxCommentLen {
		return "", Invalid("comment is too long")
	}
	return s, nil
}

// ExpensePatch is a partial update; nil fields are left unchanged.
type ExpensePatch struct {
	Amount   *Money
	Category *Category
	SpentAt  *time.Time
	Comment  *string
}

func (p *ExpensePatch) Empty() bool {
	return p.Amount == nil && p.Category == nil && p.SpentAt == nil && p.Comment == nil
}

func (p *ExpensePatch) Normalize() error {
	if p.Amount != nil && (*p.Amount <= 0 || *p.Amount > MaxAmount) {
		return Invalid("amount must be positive and reasonable")
	}
	if p.Category != nil && !p.Category.Valid() {
		return Invalid("unknown category: " + string(*p.Category))
	}
	if p.SpentAt != nil {
		d := DateOnly(*p.SpentAt)
		p.SpentAt = &d
	}
	if p.Comment != nil {
		c, err := normalizeComment(*p.Comment)
		if err != nil {
			return err
		}
		p.Comment = &c
	}
	return nil
}

// ExpenseFilter selects expenses. Zero values mean "no restriction".
type ExpenseFilter struct {
	UserIDs    []int64
	Categories []Category
	From, To   *time.Time // inclusive dates
	Query      string     // case-insensitive substring of comment
	Limit      int
	Offset     int
	Sort       Sort
}

type Sort string

const (
	SortNewest    Sort = ""           // by spent_at, newest first
	SortOldest    Sort = "oldest"     // by spent_at, oldest first
	SortLastAdded Sort = "last_added" // by creation time, most recent first
)

type CategoryTotal struct {
	Category Category
	Amount   Money
	Count    int
}

type Summary struct {
	Total      Money
	Count      int
	ByCategory []CategoryTotal
}

type User struct {
	TelegramID int64
	Username   string
	FirstName  string
	LastName   string
	Name       string     // name chosen for the family budget; empty means the Telegram name
	IsMember   bool       // joined the family via an invite link
	JoinedAt   *time.Time // when the user last joined
}

var ErrInvalidInvite = errors.New("invalid invite code")

// MaxNameLen limits a member's chosen name.
const MaxNameLen = 40

// TelegramName is the name from the Telegram profile.
func (u User) TelegramName() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

// DisplayName returns a human-readable name for the user.
func (u User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	if name := u.TelegramName(); name != "" {
		return name
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return "ID " + itoa(u.TelegramID)
}

// ParseName validates a member's name: "  маша " → "Маша". Digits are not allowed,
// which keeps names apart from expenses like "кофе 350".
func ParseName(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", Invalid("name must be valid UTF-8")
	}
	name := strings.Join(strings.Fields(s), " ")
	if name == "" {
		return "", Invalid("name is empty")
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		return "", Invalid("name is too long")
	}
	hasLetter := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			return "", Invalid("name must not contain digits")
		case r == ' ' || r == '-' || r == '\'' || r == '.':
		default:
			return "", Invalid("name may contain only letters, spaces, hyphens and dots")
		}
	}
	if !hasLetter {
		return "", Invalid("name must contain letters")
	}
	first, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(first)) + name[size:], nil
}

// DateOnly strips the time part, keeping the calendar date as midnight UTC.
func DateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ParseDate parses a YYYY-MM-DD date.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, Invalid("invalid date, expected YYYY-MM-DD: " + s)
	}
	return t, nil
}
