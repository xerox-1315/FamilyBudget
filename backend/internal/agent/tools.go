package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"

	"familybudget/internal/domain"
)

func categoryEnum() []string {
	out := make([]string, len(domain.Categories))
	for i, c := range domain.Categories {
		out[i] = string(c)
	}
	return out
}

func schema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type obj = map[string]any

func merge(a, b obj) obj {
	out := obj{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func toolDefinitions() []openai.Tool {
	// Descriptions are short and in English to save tokens (see systemPrompt).
	date := obj{"type": "string", "description": "YYYY-MM-DD"}
	category := obj{"type": "string", "enum": categoryEnum()}
	amount := obj{"type": "number", "description": "rubles, e.g. 350 or 349.90"}
	period := obj{
		"date_from": date,
		"date_to":   date,
		"category":  category,
		"query":     obj{"type": "string", "description": "substring of comment, e.g. word stem"},
	}
	listing := obj{
		"limit": obj{"type": "integer", "description": "default 20, max 50"},
		"order": obj{"type": "string", "enum": []string{"newest", "oldest", "last_added"}, "description": "newest/oldest by date; last_added by creation time"},
	}

	defs := []openai.FunctionDefinition{
		{
			Name:        "add_expenses",
			Description: "Add one or more expenses.",
			Parameters: schema(obj{
				"type": "object",
				"properties": obj{
					"expenses": obj{
						"type": "array",
						"items": obj{
							"type": "object",
							"properties": obj{
								"amount":   amount,
								"category": category,
								"spent_at": obj{"type": "string", "description": "YYYY-MM-DD, default today"},
								"comment":  obj{"type": "string", "description": "what was bought"},
							},
							"required": []string{"amount", "category", "comment"},
						},
					},
				},
				"required": []string{"expenses"},
			}),
		},
		{
			Name:        "show_expenses",
			Description: "Show the user a list of expenses. The list is sent to the user directly.",
			Parameters:  schema(obj{"type": "object", "properties": merge(period, listing)}),
		},
		{
			Name:        "summarize_expenses",
			Description: "Show the user the total and per-category sums. Sent to the user directly.",
			Parameters:  schema(obj{"type": "object", "properties": period}),
		},
		{
			Name:        "find_expenses",
			Description: "Look up expenses with ids, to change or delete them. Not shown to the user.",
			Parameters:  schema(obj{"type": "object", "properties": merge(period, listing)}),
		},
		{
			Name:        "update_expense",
			Description: "Change an expense by id; pass only changed fields.",
			Parameters: schema(obj{
				"type": "object",
				"properties": obj{
					"id":       obj{"type": "integer"},
					"amount":   amount,
					"category": category,
					"spent_at": date,
					"comment":  obj{"type": "string"},
				},
				"required": []string{"id"},
			}),
		},
		{
			Name:        "set_my_name",
			Description: "Change how the user is named in the family budget. Empty name restores the Telegram name.",
			Parameters: schema(obj{
				"type":       "object",
				"properties": obj{"name": obj{"type": "string"}},
				"required":   []string{"name"},
			}),
		},
		{
			Name:        "request_delete_expenses",
			Description: "Ask the user to confirm deleting expenses by id. Nothing is deleted until they confirm.",
			Parameters: schema(obj{
				"type":       "object",
				"properties": obj{"ids": obj{"type": "array", "items": obj{"type": "integer"}}},
				"required":   []string{"ids"},
			}),
		},
	}

	tools := make([]openai.Tool, len(defs))
	for i := range defs {
		tools[i] = openai.Tool{Type: openai.ToolTypeFunction, Function: &defs[i]}
	}
	return tools
}

// flexAmount accepts a JSON number or a numeric string and keeps its exact decimal text.
type flexAmount struct {
	set   bool
	value domain.Money
}

func (f *flexAmount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		return nil
	}
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		s = n.String()
	}
	m, err := domain.ParseRubles(s)
	if err != nil {
		return err
	}
	f.set, f.value = true, m
	return nil
}

// flexID accepts an integer or a numeric string.
type flexID int64

func (f *flexID) UnmarshalJSON(b []byte) error {
	var n json.Number
	b = bytes.Trim(bytes.TrimSpace(b), `"`)
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	v, err := n.Int64()
	if err != nil {
		return err
	}
	*f = flexID(v)
	return nil
}

type expenseView struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Amount   string `json:"amount"`
	Category string `json:"category"`
	Comment  string `json:"comment,omitempty"`
}

func view(e domain.Expense) expenseView {
	return expenseView{
		ID: e.ID, Date: e.SpentAt.Format(domain.DateLayout), Amount: e.Amount.String(), Category: e.Category.Name(), Comment: e.Comment,
	}
}

// call is the context of one tool execution.
type call struct {
	userID  int64
	today   time.Time
	pending *pendingDelete // set by request_delete_expenses
	// done collects ready user-facing replies from tools whose result needs no further
	// wording by the model (add, update, show, summarize) in the current step.
	done []string
}

func (a *Agent) runTool(ctx context.Context, c *call, name, args string) string {
	var (
		res any
		err error
	)
	switch name {
	case "add_expenses":
		res, err = a.toolAdd(ctx, c, args)
	case "find_expenses":
		res, err = a.toolFind(ctx, c, args, false)
	case "show_expenses":
		res, err = a.toolFind(ctx, c, args, true)
	case "summarize_expenses":
		res, err = a.toolSummarize(ctx, c, args)
	case "update_expense":
		res, err = a.toolUpdate(ctx, c, args)
	case "request_delete_expenses":
		res, err = a.toolRequestDelete(ctx, c, args)
	case "set_my_name":
		res, err = a.toolSetName(ctx, c, args)
	default:
		err = domain.Invalid("unknown tool " + name)
	}
	if err != nil {
		var verr *domain.ValidationError
		msg := "внутренняя ошибка, попробуй ещё раз"
		switch {
		case errors.As(err, &verr):
			msg = verr.Msg
		case errors.Is(err, domain.ErrNotFound):
			msg = "трата не найдена"
		default:
			a.log.Error("tool failed", "tool", name, "err", err)
		}
		res = obj{"error": msg}
	}
	b, _ := json.Marshal(res)
	return string(b)
}

func decodeArgs(args string, dst any) error {
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	if err := json.Unmarshal([]byte(args), dst); err != nil {
		return domain.Invalid("invalid arguments: " + err.Error())
	}
	return nil
}

func optDate(s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	d, err := domain.ParseDate(s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (a *Agent) toolAdd(ctx context.Context, c *call, args string) (any, error) {
	var in struct {
		Expenses []struct {
			Amount   flexAmount `json:"amount"`
			Category string     `json:"category"`
			SpentAt  string     `json:"spent_at"`
			Comment  string     `json:"comment"`
		} `json:"expenses"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return nil, err
	}
	items := make([]domain.NewExpense, 0, len(in.Expenses))
	for _, e := range in.Expenses {
		if !e.Amount.set {
			return nil, domain.Invalid("amount is required")
		}
		spent := c.today
		if d, err := optDate(e.SpentAt); err != nil {
			return nil, err
		} else if d != nil {
			spent = *d
		}
		if spent.After(c.today.AddDate(0, 0, 1)) {
			return nil, domain.Invalid("spent_at is in the future: " + e.SpentAt)
		}
		items = append(items, domain.NewExpense{
			UserID: c.userID, Amount: e.Amount.value, Category: domain.Category(e.Category),
			SpentAt: spent, Comment: e.Comment,
		})
	}
	created, err := a.svc.Create(ctx, items)
	if err != nil {
		return nil, err
	}
	out := make([]expenseView, len(created))
	for i, e := range created {
		out[i] = view(e)
	}
	if len(created) == 1 {
		c.done = append(c.done, "Записал: "+line(created[0], c.today))
	} else {
		var b strings.Builder
		b.WriteString("Записал:")
		for _, e := range created {
			b.WriteString("\n• " + line(e, c.today))
		}
		c.done = append(c.done, b.String())
	}
	return obj{"added": out}, nil
}

type filterArgs struct {
	DateFrom string `json:"date_from"`
	DateTo   string `json:"date_to"`
	Category string `json:"category"`
	Query    string `json:"query"`
	Limit    int    `json:"limit"`
	Order    string `json:"order"`
}

// filter builds a filter that is always restricted to the calling user.
func (f filterArgs) filter(userID int64) (domain.ExpenseFilter, error) {
	out := domain.ExpenseFilter{UserIDs: []int64{userID}, Query: f.Query}
	var err error
	if out.From, err = optDate(f.DateFrom); err != nil {
		return out, err
	}
	if out.To, err = optDate(f.DateTo); err != nil {
		return out, err
	}
	if f.Category != "" {
		out.Categories = []domain.Category{domain.Category(f.Category)}
	}
	switch f.Order {
	case "", "newest":
	case "oldest":
		out.Sort = domain.SortOldest
	case "last_added":
		out.Sort = domain.SortLastAdded
	default:
		return out, domain.Invalid("unknown order: " + f.Order)
	}
	return out, nil
}

// toolFind looks expenses up; with show it also renders the list for the user.
func (a *Agent) toolFind(ctx context.Context, c *call, args string, show bool) (any, error) {
	var in filterArgs
	if err := decodeArgs(args, &in); err != nil {
		return nil, err
	}
	f, err := in.filter(c.userID)
	if err != nil {
		return nil, err
	}
	f.Limit = in.Limit
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 50 {
		f.Limit = 50
	}
	items, err := a.svc.List(ctx, f)
	if err != nil {
		return nil, err
	}
	sum, err := a.svc.Summarize(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]expenseView, len(items))
	for i, e := range items {
		out[i] = view(e)
	}
	if show {
		c.done = append(c.done, renderList(items, sum, in, c.today))
	}
	return obj{"total_found": sum.Count, "total_amount": sum.Total.String(), "returned": len(out), "expenses": out}, nil
}

func renderList(items []domain.Expense, sum domain.Summary, in filterArgs, today time.Time) string {
	scope := scopeLabel(in, today)
	if len(items) == 0 {
		return "Трат " + scope + " не нашлось."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Траты %s — %s, %d %s:", scope, sum.Total.String(), sum.Count, plural(sum.Count, "трата", "траты", "трат"))
	for _, e := range items {
		what := e.Comment
		if what == "" {
			what = e.Category.Name()
		}
		fmt.Fprintf(&b, "\n• %s — %s — %s", humanDate(e.SpentAt, today), what, e.Amount.String())
	}
	if rest := sum.Count - len(items); rest > 0 {
		fmt.Fprintf(&b, "\n…и ещё %d %s", rest, plural(rest, "трата", "траты", "трат"))
	}
	return b.String()
}

func renderSummary(sum domain.Summary, in filterArgs, today time.Time) string {
	scope := scopeLabel(in, today)
	if sum.Count == 0 {
		return "Трат " + scope + " нет."
	}
	head := fmt.Sprintf("Потрачено %s: %s (%d %s)", scope, sum.Total.String(), sum.Count, plural(sum.Count, "трата", "траты", "трат"))
	if len(sum.ByCategory) < 2 {
		return head + "."
	}
	var b strings.Builder
	b.WriteString(head + ":")
	for _, t := range sum.ByCategory {
		fmt.Fprintf(&b, "\n• %s — %s", t.Category.Name(), t.Amount.String())
	}
	return b.String()
}

// scopeLabel describes a filter in words: "на «кофе» в категории «Еда» за 01.10–16.10".
func scopeLabel(in filterArgs, today time.Time) string {
	var parts []string
	if q := strings.TrimSpace(in.Query); q != "" {
		parts = append(parts, "по запросу «"+q+"»")
	}
	if cat := domain.Category(in.Category); cat.Valid() {
		parts = append(parts, "в категории «"+cat.Name()+"»")
	}
	from, _ := optDate(in.DateFrom)
	to, _ := optDate(in.DateTo)
	switch {
	case from == nil && to == nil:
		parts = append(parts, "за всё время")
	case from != nil && to != nil && from.Equal(*to):
		parts = append(parts, "за "+humanDate(*from, today))
	case from != nil && to != nil:
		parts = append(parts, "за "+humanDate(*from, today)+" – "+humanDate(*to, today))
	case from != nil:
		parts = append(parts, "с "+humanDate(*from, today))
	default:
		parts = append(parts, "по "+humanDate(*to, today))
	}
	return strings.Join(parts, " ")
}

func (a *Agent) toolSummarize(ctx context.Context, c *call, args string) (any, error) {
	var in filterArgs
	if err := decodeArgs(args, &in); err != nil {
		return nil, err
	}
	f, err := in.filter(c.userID)
	if err != nil {
		return nil, err
	}
	sum, err := a.svc.Summarize(ctx, f)
	if err != nil {
		return nil, err
	}
	c.done = append(c.done, renderSummary(sum, in, c.today))
	type catView struct {
		Category string `json:"category"`
		Amount   string `json:"amount"`
		Count    int    `json:"count"`
	}
	cats := make([]catView, len(sum.ByCategory))
	for i, t := range sum.ByCategory {
		cats[i] = catView{Category: t.Category.Name(), Amount: t.Amount.String(), Count: t.Count}
	}
	return obj{
		"period_from": in.DateFrom, "period_to": in.DateTo,
		"total_amount": sum.Total.String(), "count": sum.Count, "by_category": cats,
	}, nil
}

func (a *Agent) toolUpdate(ctx context.Context, c *call, args string) (any, error) {
	var in struct {
		ID       flexID     `json:"id"`
		Amount   flexAmount `json:"amount"`
		Category *string    `json:"category"`
		SpentAt  *string    `json:"spent_at"`
		Comment  *string    `json:"comment"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return nil, err
	}
	var patch domain.ExpensePatch
	if in.Amount.set {
		patch.Amount = &in.Amount.value
	}
	if in.Category != nil && *in.Category != "" {
		cat := domain.Category(*in.Category)
		patch.Category = &cat
	}
	if in.SpentAt != nil && *in.SpentAt != "" {
		d, err := domain.ParseDate(*in.SpentAt)
		if err != nil {
			return nil, err
		}
		patch.SpentAt = &d
	}
	patch.Comment = in.Comment
	owner := c.userID
	e, err := a.svc.Update(ctx, int64(in.ID), &owner, patch)
	if err != nil {
		return nil, err
	}
	c.done = append(c.done, "Исправил: "+line(e, c.today))
	return obj{"updated": view(e)}, nil
}

func (a *Agent) toolRequestDelete(ctx context.Context, c *call, args string) (any, error) {
	var in struct {
		IDs []flexID `json:"ids"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return nil, err
	}
	if len(in.IDs) == 0 {
		return nil, domain.Invalid("ids is required")
	}
	var found []domain.Expense
	seen := map[int64]bool{}
	for _, id := range in.IDs {
		if seen[int64(id)] {
			continue
		}
		seen[int64(id)] = true
		e, err := a.svc.Get(ctx, int64(id))
		if errors.Is(err, domain.ErrNotFound) || (err == nil && e.UserID != c.userID) {
			// Someone else's expense looks exactly like a missing one.
			return nil, fmt.Errorf("%w: id %d", domain.ErrNotFound, id)
		}
		if err != nil {
			return nil, err
		}
		found = append(found, e)
	}
	ids := make([]int64, len(found))
	for i, e := range found {
		ids[i] = e.ID
	}
	c.pending = &pendingDelete{ids: ids, summary: describe(found, c.today)}
	return obj{"status": "awaiting_user_confirmation"}, nil
}

func (a *Agent) toolSetName(ctx context.Context, c *call, args string) (any, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return nil, err
	}
	u, err := a.svc.Rename(ctx, c.userID, in.Name)
	if err != nil {
		return nil, err
	}
	if u.Name == "" {
		c.done = append(c.done, "Хорошо, снова подписываю тебя именем из Telegram: «"+u.DisplayName()+"».")
	} else {
		c.done = append(c.done, "Запомнил, теперь ты в семейном бюджете — «"+u.Name+"».")
	}
	return obj{"name": u.DisplayName()}, nil
}

// describe renders expenses as a short human-readable list.
func describe(items []domain.Expense, today time.Time) string {
	if len(items) == 1 {
		return line(items[0], today)
	}
	var b strings.Builder
	for _, e := range items {
		b.WriteString("\n• " + line(e, today))
	}
	return b.String()
}
