package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"familybudget/internal/agent"
	"familybudget/internal/domain"
	"familybudget/internal/service"
	"familybudget/internal/storage/memstore"
)

const (
	userID  int64 = 1 // the person talking to the bot
	otherID int64 = 2 // another family member whose expenses must stay untouched
)

// Result is the outcome of one run of one case.
type Result struct {
	Model    string
	Case     *Case
	Repeat   int
	Passed   bool
	Failures []string
	Replies  []string
	Tools    []string
	Err      error
	Latency  time.Duration // model time per message, rate-limit waits excluded
	Calls    int           // LLM calls per message
	Tokens   int           // tokens per message
	Skipped  bool          // the model's daily quota was exhausted before this run
}

type Options struct {
	Suite   *Suite
	Client  agent.ChatClient
	Models  []string
	Repeat  int
	Filter  func(*Case) bool
	Loc     *time.Location
	OnStart func(model string, c *Case, repeat int)
	OnDone  func(Result)
}

// Run evaluates every model on every case. Models run concurrently: providers usually
// count rate limits per model, so this does not make them hit limits sooner.
func Run(ctx context.Context, opt Options) []Result {
	var (
		mu      sync.Mutex
		results []Result
		wg      sync.WaitGroup
	)
	for _, model := range opt.Models {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := &meter{inner: opt.Client}
			for i := range opt.Suite.Cases {
				c := &opt.Suite.Cases[i]
				if opt.Filter != nil && !opt.Filter(c) {
					continue
				}
				for r := 1; r <= opt.Repeat; r++ {
					var res Result
					if m.exhausted() {
						res = Result{Model: model, Case: c, Repeat: r, Skipped: true, Err: ErrDailyLimit}
					} else {
						if opt.OnStart != nil {
							opt.OnStart(model, c, r)
						}
						res = runCase(ctx, opt, m, model, c, r)
					}
					mu.Lock()
					results = append(results, res)
					mu.Unlock()
					if opt.OnDone != nil {
						opt.OnDone(res)
					}
				}
			}
		}()
	}
	wg.Wait()
	return results
}

func runCase(ctx context.Context, opt Options, m *meter, model string, c *Case, repeat int) Result {
	res := Result{Model: model, Case: c, Repeat: repeat}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	store := memstore.New()
	svc := service.New(store)
	seedIDs, err := setup(ctx, svc, opt.Suite, c)
	if err != nil {
		res.Err = fmt.Errorf("setup: %w", err)
		return res
	}

	now := opt.Suite.now(opt.Loc)
	ag := agent.New(svc, agent.Options{
		LLM:      m,
		Models:   []string{model},
		Location: opt.Loc,
		Now:      func() time.Time { return now },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnToolCall: func(_ int64, name, _, _ string) {
			res.Tools = append(res.Tools, name)
		},
	})

	before := m.snapshot()
	for _, turn := range c.Turns {
		reply, err := ag.Reply(ctx, userID, turn)
		if err != nil {
			res.Err = err
			break
		}
		res.Replies = append(res.Replies, reply)
	}
	used := m.snapshot().sub(before)
	if n := len(res.Replies); n > 0 {
		res.Latency = used.busy / time.Duration(n)
		res.Calls = used.calls / n
		res.Tokens = used.tokens / n
	}
	if res.Err != nil {
		res.Failures = []string{"ошибка LLM: " + res.Err.Error()}
		return res
	}

	res.Failures = grade(ctx, svc, ag, opt.Suite, c, seedIDs, res)
	res.Passed = len(res.Failures) == 0
	return res
}

// setup creates both users and their seed expenses; it returns the user's seed ids in order.
func setup(ctx context.Context, svc *service.Expenses, s *Suite, c *Case) ([]int64, error) {
	code, err := svc.InviteCode(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range []domain.User{{TelegramID: userID, FirstName: "Тестер"}, {TelegramID: otherID, FirstName: "Мама"}} {
		if _, err := svc.Join(ctx, u, code); err != nil {
			return nil, err
		}
	}
	var ids []int64
	for _, e := range c.Seed {
		ne, err := e.toNew(userID)
		if err != nil {
			return nil, err
		}
		created, err := svc.Create(ctx, []domain.NewExpense{ne})
		if err != nil {
			return nil, err
		}
		ids = append(ids, created[0].ID)
	}
	for _, e := range s.Others {
		ne, err := e.toNew(otherID)
		if err != nil {
			return nil, err
		}
		if _, err := svc.Create(ctx, []domain.NewExpense{ne}); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// grade compares the final state with the expectation and returns what is wrong.
func grade(ctx context.Context, svc *service.Expenses, ag *agent.Agent, s *Suite, c *Case, seedIDs []int64, res Result) []string {
	var fails []string
	failf := func(format string, args ...any) { fails = append(fails, fmt.Sprintf(format, args...)) }

	mine, err := svc.List(ctx, domain.ExpenseFilter{UserIDs: []int64{userID}})
	if err != nil {
		return []string{err.Error()}
	}
	actual := map[int64]domain.Expense{}
	for _, e := range mine {
		actual[e.ID] = e
	}

	// Resolve which seed expense each Match refers to.
	seed := make([]domain.Expense, len(c.Seed))
	for i, e := range c.Seed {
		ne, _ := e.toNew(userID)
		seed[i] = domain.Expense{ID: seedIDs[i], UserID: userID, Amount: ne.Amount, Category: ne.Category, SpentAt: ne.SpentAt, Comment: ne.Comment}
	}
	find := func(m Match) (domain.Expense, bool) {
		var found []domain.Expense
		for _, e := range seed {
			if m.matches(e) {
				found = append(found, e)
			}
		}
		if len(found) != 1 {
			failf("ошибка в кейсе: %q соответствует %d тратам из seed", m, len(found))
			return domain.Expense{}, false
		}
		return found[0], true
	}

	handled := map[int64]bool{}
	for _, m := range c.Expect.Deleted {
		if e, ok := find(m); ok {
			handled[e.ID] = true
			if _, still := actual[e.ID]; still {
				failf("не удалено: %s", describe(e))
			}
		}
	}
	for _, u := range c.Expect.Updated {
		e, ok := find(u.Match)
		if !ok {
			continue
		}
		handled[e.ID] = true
		got, exists := actual[e.ID]
		if !exists {
			failf("удалено вместо изменения: %s", describe(e))
			continue
		}
		for _, d := range u.Set.check(got) {
			failf("%s: %s", u.Match, d)
		}
		// Fields that were not supposed to change.
		if u.Set.Amount == "" && got.Amount != e.Amount ||
			u.Set.Category == "" && got.Category != e.Category ||
			u.Set.Date == "" && !got.SpentAt.Equal(e.SpentAt) ||
			u.Set.Comment == "" && got.Comment != e.Comment {
			failf("%s: изменено лишнее, стало %s", u.Match, describe(got))
		}
	}
	for _, e := range seed {
		if handled[e.ID] {
			continue
		}
		got, exists := actual[e.ID]
		switch {
		case !exists:
			failf("лишнее удаление: %s", describe(e))
		case describe(got) != describe(e):
			failf("лишнее изменение: %s → %s", describe(e), describe(got))
		}
	}

	// New expenses must match Added one-to-one.
	var added []domain.Expense
	for _, e := range mine {
		if !slices.Contains(seedIDs, e.ID) {
			added = append(added, e)
		}
	}
	if len(added) != len(c.Expect.Added) {
		var got []string
		for _, e := range added {
			got = append(got, describe(e))
		}
		failf("добавлено %d трат, ожидалось %d: %s", len(added), len(c.Expect.Added), strings.Join(got, "; "))
	} else if !matchAll(c.Expect.Added, added) {
		// Explain each expectation by its closest added expense.
		for _, w := range c.Expect.Added {
			var best []string
			for i, e := range added {
				if d := w.check(e); i == 0 || len(d) < len(best) {
					best = d
				}
			}
			if len(best) > 0 {
				failf("добавленная трата: %s", strings.Join(best, ", "))
			}
		}
	}

	// The other member's expenses are never touched.
	theirs, _ := svc.List(ctx, domain.ExpenseFilter{UserIDs: []int64{otherID}})
	if len(theirs) != len(s.Others) {
		failf("НАРУШЕНА ИЗОЛЯЦИЯ: у другого члена семьи %d трат вместо %d", len(theirs), len(s.Others))
	}

	// Pending deletion.
	var wantPending []int64
	for _, m := range c.Expect.Pending {
		if e, ok := find(m); ok {
			wantPending = append(wantPending, e.ID)
		}
	}
	gotPending := ag.PendingDeletion(userID)
	slices.Sort(wantPending)
	slices.Sort(gotPending)
	if !slices.Equal(wantPending, gotPending) {
		failf("ожидает подтверждения удаления: %v, ожидалось %v", names(gotPending, seed), names(wantPending, seed))
	}

	for _, t := range c.Expect.ToolsInclude {
		if !slices.Contains(res.Tools, t) {
			failf("не вызван %s (вызваны: %s)", t, toolList(res.Tools))
		}
	}
	for _, t := range c.Expect.ToolsExclude {
		if slices.Contains(res.Tools, t) {
			failf("вызван запрещённый %s", t)
		}
	}

	last := normalize(res.Replies[len(res.Replies)-1])
	for _, want := range c.Expect.Reply {
		if !strings.Contains(last, normalize(want)) {
			failf("в ответе нет %q", want)
		}
	}
	if len(c.Expect.ReplyAny) > 0 && !slices.ContainsFunc(c.Expect.ReplyAny, func(w string) bool {
		return strings.Contains(last, normalize(w))
	}) {
		failf("в ответе нет ни одного из %q", c.Expect.ReplyAny)
	}
	for _, bad := range c.Expect.ReplyNot {
		if strings.Contains(last, normalize(bad)) {
			failf("в ответе есть %q", bad)
		}
	}

	if c.Expect.Name != "" {
		if u, err := svc.User(ctx, userID); err != nil || u.Name != c.Expect.Name {
			failf("имя %q, ожидалось %q", u.Name, c.Expect.Name)
		}
	}
	return fails
}

// matchAll reports whether wants can be paired with distinct expenses.
func matchAll(wants []Want, got []domain.Expense) bool {
	used := make([]bool, len(got))
	var try func(i int) bool
	try = func(i int) bool {
		if i == len(wants) {
			return true
		}
		for j, e := range got {
			if !used[j] && len(wants[i].check(e)) == 0 {
				used[j] = true
				if try(i + 1) {
					return true
				}
				used[j] = false
			}
		}
		return false
	}
	return try(0)
}

func names(ids []int64, seed []domain.Expense) []string {
	out := []string{}
	for _, id := range ids {
		for _, e := range seed {
			if e.ID == id {
				out = append(out, e.Comment)
			}
		}
	}
	return out
}

func toolList(tools []string) string {
	if len(tools) == 0 {
		return "ничего"
	}
	return strings.Join(tools, ", ")
}

// normalize makes reply checks robust to case and non-breaking spaces in amounts.
func normalize(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", " ", " ", " ", "ё", "е").Replace(s))
}

// IsSetupError reports whether the result failed before reaching the model.
func IsSetupError(r Result) bool {
	return r.Err != nil && strings.HasPrefix(r.Err.Error(), "setup:") && !errors.Is(r.Err, ErrDailyLimit)
}
