// Package eval measures how well the expense agent understands people, running scripted
// dialogues against a real LLM and grading the resulting state of the expenses.
package eval

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"familybudget/internal/domain"
)

// Suite is a cases file.
type Suite struct {
	Today  string    `yaml:"today"`  // fixed "today" for every case, YYYY-MM-DD
	Others []Expense `yaml:"others"` // another family member's expenses, must never change
	Cases  []Case    `yaml:"cases"`
	Base   any       `yaml:"base"` // holder for YAML anchors, not used directly
}

// Case is one scripted dialogue and what it should lead to.
type Case struct {
	ID       string    `yaml:"id"`
	Category string    `yaml:"category"`
	Note     string    `yaml:"note"`
	Seed     []Expense `yaml:"seed"`  // the user's expenses before the dialogue, in creation order
	Turns    []string  `yaml:"turns"` // user messages
	Expect   Expect    `yaml:"expect"`
}

// Expense is an expense in a cases file. Amount is in rubles.
type Expense struct {
	Comment  string `yaml:"comment"`
	Amount   string `yaml:"amount"`
	Category string `yaml:"category"`
	Date     string `yaml:"date"`
}

// Expect describes the outcome. Seed expenses not mentioned must stay unchanged;
// so must the other member's expenses.
type Expect struct {
	Added   []Want   `yaml:"added"`
	Updated []Update `yaml:"updated"`
	Deleted []Match  `yaml:"deleted"`
	// Pending lists seed expenses awaiting deletion confirmation after the last turn;
	// when empty, nothing may be pending.
	Pending []Match `yaml:"pending"`

	ToolsInclude []string `yaml:"tools_include"` // each must be called at least once
	ToolsExclude []string `yaml:"tools_exclude"` // none may be called

	Reply    []string `yaml:"reply"`     // last reply must contain all of these
	ReplyAny []string `yaml:"reply_any"` // ...and at least one of these
	ReplyNot []string `yaml:"reply_not"` // ...and none of these

	Name string `yaml:"name"` // the user's chosen name afterwards
}

// Want matches an expense. Empty fields match anything; "a|b" lists alternatives.
// Comment matches as a case-insensitive substring.
type Want struct {
	Comment  string `yaml:"comment"`
	Amount   string `yaml:"amount"`
	Category string `yaml:"category"`
	Date     string `yaml:"date"`
}

// Match identifies a seed expense by comment substring and, if needed, date.
type Match struct {
	Comment string `yaml:"comment"`
	Date    string `yaml:"date"`
}

type Update struct {
	Match Match `yaml:"match"`
	Set   Want  `yaml:"set"` // only listed fields may change
}

// Load reads and validates a cases file.
func Load(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Suite
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := domain.ParseDate(s.Today); err != nil {
		return nil, fmt.Errorf("today: %w", err)
	}
	seen := map[string]bool{}
	for i, c := range s.Cases {
		if c.ID == "" || seen[c.ID] {
			return nil, fmt.Errorf("case #%d: missing or duplicate id %q", i+1, c.ID)
		}
		seen[c.ID] = true
		if len(c.Turns) == 0 {
			return nil, fmt.Errorf("case %s: no turns", c.ID)
		}
		for _, e := range append(append([]Expense{}, c.Seed...), s.Others...) {
			if _, err := e.toNew(0); err != nil {
				return nil, fmt.Errorf("case %s: seed %+v: %w", c.ID, e, err)
			}
		}
	}
	return &s, nil
}

func (e Expense) toNew(userID int64) (domain.NewExpense, error) {
	amount, err := domain.ParseRubles(e.Amount)
	if err != nil {
		return domain.NewExpense{}, err
	}
	date, err := domain.ParseDate(e.Date)
	if err != nil {
		return domain.NewExpense{}, err
	}
	cat := domain.Category(e.Category)
	if !cat.Valid() {
		return domain.NewExpense{}, fmt.Errorf("unknown category %q", e.Category)
	}
	return domain.NewExpense{UserID: userID, Amount: amount, Category: cat, SpentAt: date, Comment: e.Comment}, nil
}

func (m Match) matches(e domain.Expense) bool {
	if m.Comment != "" && !strings.Contains(strings.ToLower(e.Comment), strings.ToLower(m.Comment)) {
		return false
	}
	return m.Date == "" || e.SpentAt.Format(domain.DateLayout) == m.Date
}

func (m Match) String() string {
	if m.Date != "" {
		return m.Comment + " (" + m.Date + ")"
	}
	return m.Comment
}

// check reports how e differs from w; empty means it matches.
func (w Want) check(e domain.Expense) []string {
	var diffs []string
	if w.Comment != "" && !anyOf(w.Comment, func(alt string) bool {
		return strings.Contains(strings.ToLower(e.Comment), strings.ToLower(alt))
	}) {
		diffs = append(diffs, fmt.Sprintf("comment %q, want ~%q", e.Comment, w.Comment))
	}
	if w.Amount != "" && !anyOf(w.Amount, func(alt string) bool {
		m, err := domain.ParseRubles(alt)
		return err == nil && m == e.Amount
	}) {
		diffs = append(diffs, fmt.Sprintf("amount %s, want %s", e.Amount.Rubles(), w.Amount))
	}
	if w.Category != "" && !anyOf(w.Category, func(alt string) bool { return string(e.Category) == alt }) {
		diffs = append(diffs, fmt.Sprintf("category %s, want %s", e.Category, w.Category))
	}
	if w.Date != "" && !anyOf(w.Date, func(alt string) bool { return e.SpentAt.Format(domain.DateLayout) == alt }) {
		diffs = append(diffs, fmt.Sprintf("date %s, want %s", e.SpentAt.Format(domain.DateLayout), w.Date))
	}
	return diffs
}

func anyOf(spec string, ok func(string) bool) bool {
	for _, alt := range strings.Split(spec, "|") {
		if ok(strings.TrimSpace(alt)) {
			return true
		}
	}
	return false
}

func describe(e domain.Expense) string {
	return fmt.Sprintf("%s %s₽ %s %s", e.Comment, e.Amount.Rubles(), e.Category, e.SpentAt.Format(domain.DateLayout))
}

// noon of the suite's "today" in the given location: the agent's clock during evals.
func (s *Suite) now(loc *time.Location) time.Time {
	d, _ := domain.ParseDate(s.Today)
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc)
}
