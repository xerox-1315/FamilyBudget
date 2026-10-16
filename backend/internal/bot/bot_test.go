package bot

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"familybudget/internal/agent"
	"familybudget/internal/domain"
	"familybudget/internal/service"
	"familybudget/internal/storage/memstore"
)

// newTestBot builds a bot without Telegram; the agent runs in fallback mode (no LLM).
func newTestBot(t *testing.T) (*Bot, *service.Expenses, string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(memstore.New())
	b := &Bot{agent: agent.New(svc, agent.Options{Logger: log}), svc: svc, log: log, locks: map[int64]*sync.Mutex{}}
	code, err := svc.InviteCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b, svc, code
}

var masha = domain.User{TelegramID: 10, FirstName: "Maria", Username: "masha"}

func TestStrangerIsTurnedAway(t *testing.T) {
	b, svc, _ := newTestBot(t)
	ctx := context.Background()
	if reply := b.respond(ctx, masha, "кофе 350"); !strings.Contains(reply, "ссылку-приглашение") {
		t.Fatalf("reply = %q", reply)
	}
	if reply := b.respond(ctx, masha, "/start wrong-code"); !strings.Contains(reply, "больше не действует") {
		t.Fatalf("reply = %q", reply)
	}
	if users, _ := svc.Users(ctx); len(users) != 0 {
		t.Fatalf("stranger stored: %+v", users)
	}
}

func TestJoinAsksForName(t *testing.T) {
	b, svc, code := newTestBot(t)
	ctx := context.Background()

	reply := b.respond(ctx, masha, "/start "+code)
	if !strings.Contains(reply, "Как тебя подписывать") || !strings.Contains(reply, "Maria") {
		t.Fatalf("join reply = %q", reply)
	}
	if reply := b.respond(ctx, masha, "меня зовут маша"); !strings.HasPrefix(reply, "Приятно познакомиться, Маша!") {
		t.Fatalf("name reply = %q", reply)
	}
	u, _ := svc.User(ctx, masha.TelegramID)
	if u.DisplayName() != "Маша" {
		t.Fatalf("display name = %q", u.DisplayName())
	}
	// The question is asked once: the next message is a regular one.
	if reply := b.respond(ctx, masha, "кофе 350"); !strings.HasPrefix(reply, "Записал: кофе") {
		t.Fatalf("expense reply = %q", reply)
	}
	// Opening the link again does not ask for the name again.
	if reply := b.respond(ctx, masha, "/start "+code); !strings.HasPrefix(reply, "Привет, Маша!") {
		t.Fatalf("rejoin reply = %q", reply)
	}
}

func TestJoinKeepTelegramName(t *testing.T) {
	b, svc, code := newTestBot(t)
	ctx := context.Background()
	b.respond(ctx, masha, "/start "+code)
	if reply := b.respond(ctx, masha, "Ок"); !strings.HasPrefix(reply, "Хорошо, оставлю «Maria»") {
		t.Fatalf("reply = %q", reply)
	}
	u, _ := svc.User(ctx, masha.TelegramID)
	if u.Name != "" || u.DisplayName() != "Maria" {
		t.Fatalf("user = %+v", u)
	}
}

func TestJoinThenRequestIsNotTakenForName(t *testing.T) {
	for _, text := range []string{"кофе 350", "покажи траты за неделю", "сколько потратил"} {
		b, svc, code := newTestBot(t)
		ctx := context.Background()
		b.respond(ctx, masha, "/start "+code)
		b.respond(ctx, masha, text)
		if u, _ := svc.User(ctx, masha.TelegramID); u.Name != "" {
			t.Errorf("%q was taken for a name: %q", text, u.Name)
		}
	}
}

func TestStartPayload(t *testing.T) {
	cases := map[string]struct {
		code string
		ok   bool
	}{
		"/start abc":         {"abc", true},
		"/start":             {"", true},
		"/start@my_bot abc":  {"abc", true},
		"/starting abc":      {"", false},
		"кофе 350":           {"", false},
		"  /start   x-Y_9  ": {"x-Y_9", true},
	}
	for in, want := range cases {
		code, ok := startPayload(in)
		if code != want.code || ok != want.ok {
			t.Errorf("startPayload(%q) = %q, %v", in, code, ok)
		}
	}
}

func TestSplit(t *testing.T) {
	text := strings.Repeat("а", 10) + "\n" + strings.Repeat("б", 10)
	parts := split(text, 15)
	if len(parts) != 2 || parts[0] != strings.Repeat("а", 10) || parts[1] != strings.Repeat("б", 10) {
		t.Fatalf("parts = %q", parts)
	}
	if parts := split("коротко", 4096); len(parts) != 1 {
		t.Fatalf("parts = %q", parts)
	}
}
