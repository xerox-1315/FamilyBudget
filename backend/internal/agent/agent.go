// Package agent turns free-form messages into expense operations using an LLM with tool calling.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"

	"familybudget/internal/domain"
	"familybudget/internal/parser"
	"familybudget/internal/service"
)

// ChatClient is the subset of the OpenAI client the agent needs; *openai.Client implements it.
type ChatClient interface {
	CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

const (
	maxSteps = 6
	// maxRateLimitWait caps how long a message waits for an LLM rate limit to reset.
	maxRateLimitWait = 20 * time.Second
)

type Agent struct {
	svc    *service.Expenses
	llm    ChatClient // nil means fallback-only mode
	models []string   // primary model first, then fallbacks
	loc    *time.Location
	log    *slog.Logger
	mem    *memory
	tools  []openai.Tool
	now    func() time.Time
	sleep  func(ctx context.Context, d time.Duration) error

	onToolCall func(userID int64, name, args, result string)
}

type Options struct {
	LLM ChatClient
	// Models lists the primary model and then fallbacks, used in turn when a model is
	// rate-limited (free tiers count limits per model).
	Models   []string
	Location *time.Location
	Logger   *slog.Logger
	Now      func() time.Time
	// OnToolCall, if set, observes every tool call (used by evals).
	OnToolCall func(userID int64, name, args, result string)
}

func New(svc *service.Expenses, opt Options) *Agent {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Location == nil {
		opt.Location = time.UTC
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	return &Agent{
		svc: svc, llm: opt.LLM, models: opt.Models, loc: opt.Location, log: opt.Logger,
		mem: newMemory(opt.Now), tools: toolDefinitions(), now: opt.Now, sleep: sleepCtx,
		onToolCall: opt.OnToolCall,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Prune drops stale conversation state. Safe to call periodically.
func (a *Agent) Prune() { a.mem.prune() }

func (a *Agent) today() time.Time { return domain.DateOnly(a.now().In(a.loc)) }

// Handle processes one message from a user and returns the reply text.
// userID always comes from the transport (Telegram), never from the model.
// If the LLM fails, simple expenses are still recorded by the fallback parser.
func (a *Agent) Handle(ctx context.Context, userID int64, text string) string {
	reply, err := a.Reply(ctx, userID, text)
	if err == nil {
		return reply
	}
	if ctx.Err() != nil {
		return "Не успел обработать сообщение, попробуй ещё раз."
	}
	a.log.Warn("llm failed, using fallback parser", "user", userID, "err", err)
	return a.fallback(ctx, userID, strings.TrimSpace(text), true)
}

// Reply is Handle without the fallback: an LLM failure is returned as an error.
// Evals use it so that a failing model is not masked by the parser.
func (a *Agent) Reply(ctx context.Context, userID int64, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "Напиши трату текстом, например: «кофе 350».", nil
	}

	if p := a.mem.takePending(userID); p != nil {
		switch {
		case isYes(text):
			return a.confirmDelete(ctx, userID, text, p), nil
		case isNo(text):
			reply := "Хорошо, ничего не удаляю."
			a.remember(userID, text, reply)
			return reply, nil
		}
		// Anything else cancels the pending deletion and is handled as a new request.
	}

	if a.llm == nil {
		return a.fallback(ctx, userID, text, false), nil
	}
	return a.runLLM(ctx, userID, text)
}

// PendingDeletion returns the ids of expenses awaiting the user's confirmation, if any.
func (a *Agent) PendingDeletion(userID int64) []int64 {
	return a.mem.peekPending(userID)
}

func (a *Agent) runLLM(ctx context.Context, userID int64, text string) (string, error) {
	c := &call{userID: userID, today: a.today()}
	turnMsgs := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: text}}
	base := append([]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleSystem, Content: a.systemPrompt()}},
		a.mem.history(userID)...)

	for step := 0; step < maxSteps; step++ {
		resp, err := a.complete(ctx, append(base, turnMsgs...))
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("empty response from LLM")
		}
		msg := resp.Choices[0].Message
		msg.ReasoningContent = ""

		if len(msg.ToolCalls) == 0 {
			reply := cleanReply(msg.Content)
			if reply == "" {
				reply = "Готово."
			}
			turnMsgs = append(turnMsgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: reply})
			a.mem.addTurn(userID, turnMsgs)
			return reply, nil
		}

		turnMsgs = append(turnMsgs, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleAssistant, Content: msg.Content, ToolCalls: msg.ToolCalls,
		})
		c.done = nil
		for _, tc := range msg.ToolCalls {
			result := a.runTool(ctx, c, tc.Function.Name, tc.Function.Arguments)
			a.log.Debug("tool call", "user", userID, "tool", tc.Function.Name, "args", tc.Function.Arguments, "result", result)
			if a.onToolCall != nil {
				a.onToolCall(userID, tc.Function.Name, tc.Function.Arguments, result)
			}
			turnMsgs = append(turnMsgs, openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleTool, ToolCallID: tc.ID, Name: tc.Function.Name, Content: result,
			})
		}

		// A deletion was requested: the question is asked by the server, not the model,
		// so the model cannot skip the confirmation step.
		if c.pending != nil {
			reply := "Удалить " + plural(len(c.pending.ids), "эту трату", "эти траты", "эти траты") + "?" +
				prefixSpace(c.pending.summary) + "\n\nОтветь «да» или «нет»."
			a.mem.setPending(userID, c.pending)
			turnMsgs = append(turnMsgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: reply})
			a.mem.addTurn(userID, turnMsgs)
			return reply, nil
		}

		// Every call in this step was a successful add or update: the server can report
		// the result itself, which saves an LLM round trip and keeps replies uniform.
		if len(c.done) == len(msg.ToolCalls) {
			reply := strings.Join(c.done, "\n")
			turnMsgs = append(turnMsgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: reply})
			a.mem.addTurn(userID, turnMsgs)
			return reply, nil
		}
	}
	return "", errors.New("too many tool steps")
}

// complete calls the LLM. A rate-limited or failing model is replaced by the next one;
// when all of them are rate-limited, it waits as long as the provider asks
// (up to maxRateLimitWait in total) and tries again.
func (a *Agent) complete(ctx context.Context, msgs []openai.ChatCompletionMessage) (openai.ChatCompletionResponse, error) {
	var waited time.Duration
	for {
		var lastErr error
		var wait time.Duration
		for i, model := range a.models {
			// Temperature is left at the provider default: reasoning models such as
			// gpt-oss tend to loop and repeat themselves at low temperatures.
			resp, err := a.llm.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model: model, Messages: msgs, Tools: a.tools,
			})
			if err == nil {
				a.log.Debug("llm call", "model", model, "prompt_tokens", resp.Usage.PromptTokens, "total_tokens", resp.Usage.TotalTokens)
				return resp, nil
			}
			if ctx.Err() != nil {
				return resp, ctx.Err()
			}
			lastErr = err
			if d, limited := rateLimitDelay(err); limited {
				a.log.Info("llm rate limited", "model", model, "retry_in", d)
				if wait == 0 || d < wait {
					wait = d
				}
				continue
			}
			if retryable(err) {
				a.log.Warn("llm call failed", "model", model, "err", err)
				if wait == 0 || wait > time.Second {
					wait = time.Second
				}
				continue
			}
			if i == 0 {
				return resp, err // the primary model is misconfigured: report it
			}
			a.log.Warn("fallback model failed", "model", model, "err", err)
		}
		if wait == 0 || waited+wait > maxRateLimitWait {
			return openai.ChatCompletionResponse{}, lastErr
		}
		if err := a.sleep(ctx, wait); err != nil {
			return openai.ChatCompletionResponse{}, err
		}
		waited += wait
	}
}

var retryInRe = regexp.MustCompile(`try again in ([0-9hm.]+s)`)

// rateLimitDelay reports whether err is a rate limit and how long to wait before retrying.
func rateLimitDelay(err error) (time.Duration, bool) {
	var apiErr *openai.APIError
	var reqErr *openai.RequestError
	var msg string
	switch {
	case errors.As(err, &apiErr) && apiErr.HTTPStatusCode == http.StatusTooManyRequests:
		msg = apiErr.Message
	case errors.As(err, &reqErr) && reqErr.HTTPStatusCode == http.StatusTooManyRequests:
		msg = string(reqErr.Body)
	default:
		return 0, false
	}
	d := 2 * time.Second
	if m := retryInRe.FindStringSubmatch(msg); m != nil {
		if parsed, err := time.ParseDuration(m[1]); err == nil {
			d = parsed + 250*time.Millisecond
		}
	}
	return max(d, 250*time.Millisecond), true
}

func retryable(err error) bool {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		// Groq answers 400 tool_use_failed when the model produced a malformed call.
		return apiErr.HTTPStatusCode >= 500 ||
			(apiErr.HTTPStatusCode == 400 && strings.Contains(fmt.Sprint(apiErr.Code), "tool_use_failed"))
	}
	var reqErr *openai.RequestError
	return errors.As(err, &reqErr) && reqErr.HTTPStatusCode >= 500
}

func (a *Agent) confirmDelete(ctx context.Context, userID int64, text string, p *pendingDelete) string {
	owner := userID
	deleted, err := a.svc.Delete(ctx, p.ids, &owner)
	var reply string
	switch {
	case err != nil:
		a.log.Error("delete failed", "user", userID, "err", err)
		reply = "Не получилось удалить, попробуй ещё раз."
	case len(deleted) == 0:
		reply = "Эти траты уже удалены."
	default:
		reply = "Удалил:" + prefixSpace(describe(deleted, a.today()))
	}
	a.remember(userID, text, reply)
	return reply
}

func (a *Agent) fallback(ctx context.Context, userID int64, text string, llmFailed bool) string {
	res, ok := parser.Parse(text, a.today())
	if !ok {
		if llmFailed {
			return "ИИ-помощник сейчас недоступен. Пока я понимаю только простые траты вида «кофе 350» или «такси 1200 вчера»."
		}
		return "Я понимаю траты вида «кофе 350» или «такси 1200 вчера». ИИ-помощник не настроен, поэтому вопросы и изменения пока недоступны."
	}
	created, err := a.svc.Create(ctx, []domain.NewExpense{{
		UserID: userID, Amount: res.Amount, Category: res.Category, SpentAt: res.SpentAt, Comment: res.Comment,
	}})
	if err != nil {
		a.log.Error("fallback create failed", "user", userID, "err", err)
		return "Не получилось сохранить трату, попробуй ещё раз."
	}
	reply := "Записал: " + line(created[0], a.today())
	a.remember(userID, text, reply)
	return reply
}

// remember stores an exchange handled without the LLM so the model sees it later.
func (a *Agent) remember(userID int64, text, reply string) {
	a.mem.addTurn(userID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: text},
		{Role: openai.ChatMessageRoleAssistant, Content: reply},
	})
}

var (
	yesRe = regexp.MustCompile(`^(да|ага|угу|давай|удаляй|удали|подтверждаю|конечно|yes|y|ок|окей|ok|\+)[.!\s]*$`)
	noRe  = regexp.MustCompile(`^(нет|не|не надо|не удаляй|отмена|отмени|стоп|no|n|-)[.!\s]*$`)
)

func isYes(s string) bool { return yesRe.MatchString(strings.ToLower(strings.TrimSpace(s))) }
func isNo(s string) bool  { return noRe.MatchString(strings.ToLower(strings.TrimSpace(s))) }

var markdownRe = regexp.MustCompile(`\*\*|__|^#+\s*`)

// cleanReply strips markdown that Telegram would show literally in plain-text messages.
func cleanReply(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(markdownRe.ReplaceAllString(l, ""), " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func prefixSpace(s string) string {
	if strings.HasPrefix(s, "\n") {
		return s
	}
	return " " + s
}

func plural(n int, one, few, many string) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		return few
	default:
		return many
	}
}

func humanDate(d, today time.Time) string {
	switch {
	case d.Equal(today):
		return "сегодня"
	case d.Equal(today.AddDate(0, 0, -1)):
		return "вчера"
	case d.Year() == today.Year():
		return d.Format("02.01")
	default:
		return d.Format("02.01.2006")
	}
}

// line renders one expense for a reply: "кофе — 350 ₽ (Еда), сегодня".
func line(e domain.Expense, today time.Time) string {
	what := e.Comment
	if what == "" {
		what = "без комментария"
	}
	return fmt.Sprintf("%s — %s (%s), %s", what, e.Amount.String(), e.Category.Name(), humanDate(e.SpentAt, today))
}
