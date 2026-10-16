package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"

	"familybudget/internal/domain"
	"familybudget/internal/service"
	"familybudget/internal/storage/memstore"
)

// scriptLLM replays a fixed sequence of responses and records the requests.
type scriptLLM struct {
	steps []func(req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
	reqs  []openai.ChatCompletionRequest
}

func (s *scriptLLM) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	s.reqs = append(s.reqs, req)
	if len(s.steps) == 0 {
		return openai.ChatCompletionResponse{}, errors.New("unexpected LLM call")
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step(req)
}

func toolCall(name, args string) func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{ID: "call_" + name, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: name, Arguments: args}}},
		}}}}, nil
	}
}

func answer(text string) func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleAssistant, Content: text,
		}}}}, nil
	}
}

const (
	alice int64 = 111
	bob   int64 = 222
)

var now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T, llm ChatClient) (*Agent, *service.Expenses) {
	t.Helper()
	store := memstore.New()
	svc := service.New(store)
	ctx := context.Background()
	for _, id := range []int64{alice, bob} {
		if err := svc.TouchUser(ctx, domain.User{TelegramID: id, FirstName: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	ag := New(svc, Options{
		LLM: llm, Models: []string{"main", "spare"}, Location: time.UTC, Now: func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ag.sleep = func(context.Context, time.Duration) error { return nil }
	return ag, svc
}

func add(t *testing.T, svc *service.Expenses, user int64, amount domain.Money, comment string) domain.Expense {
	t.Helper()
	e, err := svc.Create(context.Background(), []domain.NewExpense{{
		UserID: user, Amount: amount, Category: domain.CategoryTransport, SpentAt: now, Comment: comment,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return e[0]
}

func expensesOf(t *testing.T, svc *service.Expenses, user int64) []domain.Expense {
	t.Helper()
	items, err := svc.List(context.Background(), domain.ExpenseFilter{UserIDs: []int64{user}})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestAddExpenses(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		toolCall("add_expenses", `{"expenses":[{"amount":350,"category":"food","comment":"кофе"},{"amount":"1200.5","category":"transport","spent_at":"2026-10-15","comment":"такси"}]}`),
	}}
	ag, svc := setup(t, llm)

	// A successful add is reported by the server without a second LLM call.
	reply := ag.Handle(context.Background(), alice, "кофе 350 и такси 1200,5 вчера")
	want := "Записал:\n• кофе — 350 ₽ (Еда), сегодня\n• такси — 1 200,50 ₽ (Транспорт), вчера"
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
	if len(llm.reqs) != 1 {
		t.Errorf("LLM called %d times", len(llm.reqs))
	}
	items := expensesOf(t, svc, alice)
	if len(items) != 2 {
		t.Fatalf("got %d expenses", len(items))
	}
	byComment := map[string]domain.Expense{}
	for _, e := range items {
		byComment[e.Comment] = e
	}
	if e := byComment["кофе"]; e.Amount != 35000 || !e.SpentAt.Equal(domain.DateOnly(now)) {
		t.Errorf("coffee = %+v", e)
	}
	if e := byComment["такси"]; e.Amount != 120050 || e.SpentAt.Format(domain.DateLayout) != "2026-10-15" {
		t.Errorf("taxi = %+v", e)
	}
}

func TestSummaryAndListAreRenderedByServer(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		toolCall("summarize_expenses", `{"date_from":"2026-10-01","date_to":"2026-10-16"}`),
		toolCall("show_expenses", `{"date_from":"2026-10-16","date_to":"2026-10-16","limit":1}`),
	}}
	ag, svc := setup(t, llm)
	add(t, svc, alice, 120000, "такси")
	add(t, svc, alice, 6000, "метро")
	add(t, svc, bob, 99900, "чужое")
	ctx := context.Background()

	got := ag.Handle(ctx, alice, "сколько я потратил в этом месяце")
	if want := "Потрачено за 01.10 – сегодня: 1 260 ₽ (2 траты)."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	got = ag.Handle(ctx, alice, "покажи последнюю трату за сегодня")
	if want := "Траты за сегодня — 1 260 ₽, 2 траты:\n• сегодня — метро — 60 ₽\n…и ещё 1 трата"; got != want {
		t.Errorf("list = %q, want %q", got, want)
	}
}

func TestSetMyName(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		toolCall("set_my_name", `{"name":"маша"}`),
	}}
	ag, svc := setup(t, llm)
	if reply := ag.Handle(context.Background(), alice, "зови меня Маша"); !strings.Contains(reply, "«Маша»") {
		t.Fatalf("reply = %q", reply)
	}
	// Only the caller's own name changes.
	if u, _ := svc.User(context.Background(), alice); u.Name != "Маша" {
		t.Fatalf("alice = %+v", u)
	}
	if u, _ := svc.User(context.Background(), bob); u.Name != "" {
		t.Fatalf("bob renamed: %+v", u)
	}
}

func TestToolErrorGoesBackToModel(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		toolCall("add_expenses", `{"expenses":[{"amount":350,"category":"cars","comment":"кофе"}]}`),
		toolCall("add_expenses", `{"expenses":[{"amount":350,"category":"food","comment":"кофе"}]}`),
	}}
	ag, svc := setup(t, llm)

	reply := ag.Handle(context.Background(), alice, "кофе 350")
	if !strings.HasPrefix(reply, "Записал: кофе") {
		t.Fatalf("reply = %q", reply)
	}
	msgs := llm.reqs[1].Messages
	if m := msgs[len(msgs)-1]; m.Role != openai.ChatMessageRoleTool || !strings.Contains(m.Content, "unknown category") {
		t.Errorf("validation error not passed back: %+v", m)
	}
	if n := len(expensesOf(t, svc, alice)); n != 1 {
		t.Fatalf("got %d expenses", n)
	}
}

func TestToolsCannotTouchOtherUsers(t *testing.T) {
	llm := &scriptLLM{}
	ag, svc := setup(t, llm)
	bobs := add(t, svc, bob, 50000, "такси")
	add(t, svc, alice, 10000, "метро")

	c := &call{userID: alice, today: domain.DateOnly(now)}
	ctx := context.Background()

	if res := ag.runTool(ctx, c, "find_expenses", `{}`); strings.Contains(res, "такси") {
		t.Errorf("find leaked another user's expense: %s", res)
	}
	if res := ag.runTool(ctx, c, "summarize_expenses", `{}`); !strings.Contains(res, `"count":1`) {
		t.Errorf("summary includes another user's expenses: %s", res)
	}
	if res := ag.runTool(ctx, c, "update_expense", `{"id":`+itoa(bobs.ID)+`,"amount":1}`); !strings.Contains(res, "не найдена") {
		t.Errorf("update of another user's expense: %s", res)
	}
	if res := ag.runTool(ctx, c, "request_delete_expenses", `{"ids":[`+itoa(bobs.ID)+`]}`); !strings.Contains(res, "не найдена") || c.pending != nil {
		t.Errorf("delete of another user's expense was staged: %s", res)
	}
	got, _ := svc.Get(ctx, bobs.ID)
	if got.Amount != 50000 {
		t.Errorf("bob's expense changed: %+v", got)
	}
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	llm := &scriptLLM{}
	ag, svc := setup(t, llm)
	taxi := add(t, svc, alice, 120000, "такси")
	ctx := context.Background()

	llm.steps = append(llm.steps,
		toolCall("find_expenses", `{"query":"такс"}`),
		toolCall("request_delete_expenses", `{"ids":[`+itoa(taxi.ID)+`]}`),
	)
	reply := ag.Handle(ctx, alice, "удали такси")
	if !strings.Contains(reply, "Удалить") || !strings.Contains(reply, "такси") {
		t.Fatalf("expected confirmation question, got %q", reply)
	}
	if len(expensesOf(t, svc, alice)) != 1 {
		t.Fatal("expense deleted before confirmation")
	}

	// "нет" cancels without calling the LLM.
	if reply := ag.Handle(ctx, alice, "нет"); !strings.Contains(reply, "ничего не удаляю") {
		t.Fatalf("reply = %q", reply)
	}
	if len(expensesOf(t, svc, alice)) != 1 {
		t.Fatal("expense deleted after refusal")
	}

	// Ask again and confirm.
	llm.steps = append(llm.steps, toolCall("request_delete_expenses", `{"ids":[`+itoa(taxi.ID)+`]}`))
	ag.Handle(ctx, alice, "всё-таки удали такси")
	if reply := ag.Handle(ctx, alice, "Да!"); !strings.HasPrefix(reply, "Удалил: такси") {
		t.Fatalf("reply = %q", reply)
	}
	if len(expensesOf(t, svc, alice)) != 0 {
		t.Fatal("expense not deleted after confirmation")
	}
	if len(llm.steps) != 0 {
		t.Errorf("unused LLM steps: %d", len(llm.steps))
	}
}

func TestPendingDeleteIsPerUser(t *testing.T) {
	llm := &scriptLLM{}
	ag, svc := setup(t, llm)
	taxi := add(t, svc, alice, 120000, "такси")
	ctx := context.Background()

	llm.steps = append(llm.steps, toolCall("request_delete_expenses", `{"ids":[`+itoa(taxi.ID)+`]}`))
	ag.Handle(ctx, alice, "удали такси")

	// Bob saying "да" must not confirm Alice's deletion; with no LLM steps left it falls back.
	ag.Handle(ctx, bob, "да")
	if len(expensesOf(t, svc, alice)) != 1 {
		t.Fatal("another user confirmed the deletion")
	}
}

func TestFallbackWhenLLMFails(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
			return openai.ChatCompletionResponse{}, errors.New("connection refused")
		},
	}}
	ag, svc := setup(t, llm)

	reply := ag.Handle(context.Background(), alice, "кофе 350")
	if !strings.HasPrefix(reply, "Записал") {
		t.Fatalf("reply = %q", reply)
	}
	items := expensesOf(t, svc, alice)
	if len(items) != 1 || items[0].Amount != 35000 || items[0].Category != domain.CategoryFood {
		t.Fatalf("items = %+v", items)
	}
}

func TestFallbackOnlyMode(t *testing.T) {
	ag, svc := setup(t, nil)
	if reply := ag.Handle(context.Background(), alice, "такси 500 вчера"); !strings.HasPrefix(reply, "Записал") {
		t.Fatalf("reply = %q", reply)
	}
	if reply := ag.Handle(context.Background(), alice, "сколько я потратил"); !strings.Contains(reply, "не настроен") {
		t.Fatalf("reply = %q", reply)
	}
	if n := len(expensesOf(t, svc, alice)); n != 1 {
		t.Fatalf("got %d expenses", n)
	}
}

func rateLimited(retryIn string) func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return openai.ChatCompletionResponse{}, &openai.APIError{
			HTTPStatusCode: 429,
			Message:        "Rate limit reached on tokens per minute (TPM). Please try again in " + retryIn + ". Need more tokens?",
		}
	}
}

func TestRateLimitSwitchesModel(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		rateLimited("7.88s"),
		answer("Привет!"),
	}}
	ag, _ := setup(t, llm)
	if reply := ag.Handle(context.Background(), alice, "привет"); reply != "Привет!" {
		t.Fatalf("reply = %q", reply)
	}
	if llm.reqs[0].Model != "main" || llm.reqs[1].Model != "spare" {
		t.Errorf("models used: %s, %s", llm.reqs[0].Model, llm.reqs[1].Model)
	}
}

func TestRateLimitWaitsWhenAllModelsLimited(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		rateLimited("7.5s"),
		rateLimited("217.5ms"),
		answer("Готово, записал."),
	}}
	ag, _ := setup(t, llm)
	var waits []time.Duration
	ag.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }

	if reply := ag.Handle(context.Background(), alice, "кофе 350"); reply != "Готово, записал." {
		t.Fatalf("reply = %q", reply)
	}
	if len(waits) != 1 || waits[0] != 467500*time.Microsecond {
		t.Errorf("waits = %v, want the shorter hint plus a margin", waits)
	}
	if llm.reqs[2].Model != "main" {
		t.Errorf("retry should start from the main model, got %s", llm.reqs[2].Model)
	}
}

func TestRateLimitGivesUpAfterMaxWait(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		rateLimited("1m30s"),
		rateLimited("45s"),
	}}
	ag, svc := setup(t, llm)
	// Waiting longer than maxRateLimitWait is pointless: fall back right away.
	if reply := ag.Handle(context.Background(), alice, "кофе 350"); !strings.HasPrefix(reply, "Записал") {
		t.Fatalf("reply = %q", reply)
	}
	if n := len(expensesOf(t, svc, alice)); n != 1 {
		t.Fatalf("fallback did not save the expense: %d", n)
	}
}

func TestHistoryIsKept(t *testing.T) {
	llm := &scriptLLM{steps: []func(openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error){
		answer("Какую сумму записать?"),
		answer("Ок."),
	}}
	ag, _ := setup(t, llm)
	ctx := context.Background()
	ag.Handle(ctx, alice, "кофе")
	ag.Handle(ctx, alice, "350")

	msgs := llm.reqs[1].Messages
	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.Role+":"+m.Content)
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "user:кофе|assistant:Какую сумму записать?|user:350") {
		t.Errorf("history not passed: %s", joined)
	}
}

func TestSystemPromptDates(t *testing.T) {
	ag, _ := setup(t, nil)
	p := ag.systemPrompt()
	for _, want := range []string{"2026-10-16 (пятница)", "week started 2026-10-12", "month started 2026-10-01"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
