package eval

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"
)

// fakeLLM answers every request with the next scripted tool call or text.
type fakeLLM struct {
	steps []openai.ChatCompletionMessage
}

func (f *fakeLLM) CreateChatCompletion(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	msg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "ок"}
	if len(f.steps) > 0 {
		msg, f.steps = f.steps[0], f.steps[1:]
	}
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: msg}},
		Usage:   openai.Usage{TotalTokens: 100},
	}, nil
}

func call(name, args string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{
		ID: "1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: name, Arguments: args},
	}}}
}

func loadSuite(t *testing.T) *Suite {
	t.Helper()
	s, err := Load("../../eval/cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func runOne(t *testing.T, s *Suite, id string, steps ...openai.ChatCompletionMessage) Result {
	t.Helper()
	var results []Result
	results = Run(context.Background(), Options{
		Suite: s, Client: &fakeLLM{steps: steps}, Models: []string{"fake"}, Repeat: 1, Loc: time.UTC,
		Filter: func(c *Case) bool { return c.ID == id },
	})
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	return results[0]
}

func TestCasesFileIsValid(t *testing.T) {
	s := loadSuite(t)
	if len(s.Cases) < 40 {
		t.Errorf("only %d cases", len(s.Cases))
	}
}

func TestGraderAcceptsCorrectBehaviour(t *testing.T) {
	s := loadSuite(t)
	r := runOne(t, s, "add-simple", call("add_expenses", `{"expenses":[{"amount":350,"category":"food","comment":"кофе"}]}`))
	if !r.Passed {
		t.Fatalf("add-simple failed: %v", r.Failures)
	}
	r = runOne(t, s, "del-confirm",
		call("find_expenses", `{"query":"такс"}`),
		call("request_delete_expenses", `{"ids":[7]}`))
	if !r.Passed {
		t.Fatalf("del-confirm failed: %v", r.Failures)
	}
	if r.Calls == 0 || r.Tokens == 0 {
		t.Errorf("metrics not collected: %+v", r)
	}
}

func TestGraderCatchesMistakes(t *testing.T) {
	s := loadSuite(t)
	cases := []struct {
		id   string
		step openai.ChatCompletionMessage
		want string
	}{
		{"add-simple", call("add_expenses", `{"expenses":[{"amount":300,"category":"food","comment":"кофе"}]}`), "amount"},
		{"add-simple", call("add_expenses", `{"expenses":[{"amount":350,"category":"other","comment":"кофе"}]}`), "category"},
		{"add-no-amount", call("add_expenses", `{"expenses":[{"amount":1,"category":"food","comment":"кофе"}]}`), "добавлено 1"},
		{"upd-last-amount", call("update_expense", `{"id":1,"amount":450}`), "лишнее изменение"},
		{"del-request", call("request_delete_expenses", `{"ids":[8]}`), "ожидает подтверждения"},
		{"sum-food-month", call("add_expenses", `{"expenses":[{"amount":1,"category":"food","comment":"x"}]}`), "запрещённый"},
		{"name-set", call("set_my_name", `{"name":"Даша"}`), "имя"},
	}
	for _, c := range cases {
		r := runOne(t, s, c.id, c.step)
		if r.Passed || !strings.Contains(strings.Join(r.Failures, "; "), c.want) {
			t.Errorf("%s: expected a failure mentioning %q, got passed=%v %v", c.id, c.want, r.Passed, r.Failures)
		}
	}
}
