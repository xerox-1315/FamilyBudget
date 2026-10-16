// Package bot connects the agent to Telegram via long polling.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"familybudget/internal/agent"
	"familybudget/internal/domain"
	"familybudget/internal/parser"
	"familybudget/internal/service"
)

const (
	maxMessageLen  = 4096
	messageTimeout = 2 * time.Minute
)

type Bot struct {
	api      *tgbot.Bot
	username string
	agent    *agent.Agent
	svc      *service.Expenses
	log      *slog.Logger

	mu     sync.Mutex
	locks  map[int64]*sync.Mutex
	naming naming
}

func New(token string, ag *agent.Agent, svc *service.Expenses, log *slog.Logger) (*Bot, error) {
	b := &Bot{agent: ag, svc: svc, log: log, locks: map[int64]*sync.Mutex{}}
	api, err := tgbot.New(token, tgbot.WithDefaultHandler(b.handle))
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	b.api = api
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	me, err := api.GetMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("telegram getMe: %w", err)
	}
	b.username = me.Username
	return b, nil
}

// Run polls Telegram until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	b.log.Info("telegram bot started")
	b.api.Start(ctx)
}

// Username returns the bot's @username, used to build invite links.
func (b *Bot) Username() string { return b.username }

func (b *Bot) handle(ctx context.Context, _ *tgbot.Bot, upd *models.Update) {
	msg := upd.Message
	if msg == nil || msg.From == nil || msg.Chat.Type != models.ChatTypePrivate {
		return
	}
	from := msg.From
	user := domain.User{TelegramID: from.ID, Username: from.Username, FirstName: from.FirstName, LastName: from.LastName}

	// Messages of one user are processed strictly one after another.
	lock := b.userLock(from.ID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(ctx, messageTimeout)
	defer cancel()
	stopTyping := b.typing(ctx, msg.Chat.ID)
	reply := b.respond(ctx, user, strings.TrimSpace(msg.Text))
	stopTyping()
	b.send(ctx, msg.Chat.ID, reply)
}

const somethingWrong = "Что-то пошло не так, попробуй чуть позже."

// respond decides the reply to a message. It does not touch Telegram, so it is testable.
func (b *Bot) respond(ctx context.Context, user domain.User, text string) string {
	// An invite link opens the bot with "/start <code>".
	if code, ok := startPayload(text); ok && code != "" {
		return b.join(ctx, user, code)
	}

	allowed, err := b.svc.Access(ctx, user)
	if err != nil {
		b.log.Error("check access", "user", user.TelegramID, "err", err)
		return somethingWrong
	}
	if !allowed {
		return "Это семейный бот учёта трат. Чтобы присоединиться, попроси у главы семьи ссылку-приглашение и открой её."
	}

	if b.naming.waiting(user.TelegramID) {
		if reply, handled := b.takeName(ctx, user, text); handled {
			return reply
		}
	}

	switch {
	case text == "":
		return "Пока я понимаю только текст. Напиши, например: «кофе 350»."
	case strings.HasPrefix(text, "/start"), strings.HasPrefix(text, "/help"):
		return greeting(b.displayName(ctx, user))
	}
	return b.agent.Handle(ctx, user.TelegramID, text)
}

func (b *Bot) join(ctx context.Context, user domain.User, code string) string {
	already, err := b.svc.Join(ctx, user, code)
	switch {
	case errors.Is(err, domain.ErrInvalidInvite):
		return "Эта ссылка-приглашение больше не действует. Попроси у главы семьи новую."
	case err != nil:
		b.log.Error("join family", "user", user.TelegramID, "err", err)
		return somethingWrong
	case already:
		return greeting(b.displayName(ctx, user))
	}
	b.log.Info("user joined the family", "user", user.TelegramID)
	b.naming.start(user.TelegramID)
	reply := "Добро пожаловать в семью! Как тебя подписывать в семейном бюджете? Напиши имя, например «Маша»."
	if tg := user.TelegramName(); tg != "" {
		reply += "\n\nИли ответь «ок», и я оставлю имя из Telegram: " + tg + "."
	}
	return reply
}

var (
	keepNameRe   = regexp.MustCompile(`^(ок|окей|ok|оставь|оставить|да|так|норм|нормально|\+|-)[.!\s]*$`)
	namePrefixRe = regexp.MustCompile(`^(?i)(меня зовут|зови меня|называй меня|я|это)\s+`)
)

// takeName handles the answer to "what's your name?". It reports false when the message
// should be processed as usual instead (for example, the user wrote an expense right away).
func (b *Bot) takeName(ctx context.Context, user domain.User, text string) (string, bool) {
	if keepNameRe.MatchString(strings.ToLower(text)) {
		b.naming.stop(user.TelegramID)
		return "Хорошо, оставлю «" + user.DisplayName() + "». " + intro, true
	}
	text = strings.TrimRight(namePrefixRe.ReplaceAllString(text, ""), ".!")
	name, err := domain.ParseName(text)
	if err == nil && (len(strings.Fields(name)) > 3 || parser.LooksLikeRequest(name)) {
		err = domain.Invalid("looks like a request, not a name")
	}
	if err != nil {
		// Not a name: the user moved on, so stop asking and handle the message normally.
		b.naming.stop(user.TelegramID)
		return "", false
	}
	if _, err := b.svc.Rename(ctx, user.TelegramID, name); err != nil {
		b.log.Error("rename", "user", user.TelegramID, "err", err)
		return somethingWrong, true
	}
	b.naming.stop(user.TelegramID)
	return "Приятно познакомиться, " + name + "! " + intro, true
}

// displayName returns the stored name of the user, falling back to the Telegram one.
func (b *Bot) displayName(ctx context.Context, user domain.User) string {
	if u, err := b.svc.User(ctx, user.TelegramID); err == nil {
		return u.DisplayName()
	}
	return user.DisplayName()
}

// startPayload extracts the parameter of "/start <payload>".
func startPayload(text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 || (fields[0] != "/start" && !strings.HasPrefix(fields[0], "/start@")) {
		return "", false
	}
	if len(fields) < 2 {
		return "", true
	}
	return fields[1], true
}

func (b *Bot) userLock(id int64) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	l, ok := b.locks[id]
	if !ok {
		l = &sync.Mutex{}
		b.locks[id] = l
	}
	return l
}

// typing shows the "typing…" status until the returned function is called.
func (b *Bot) typing(ctx context.Context, chatID int64) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			_, _ = b.api.SendChatAction(ctx, &tgbot.SendChatActionParams{ChatID: chatID, Action: models.ChatActionTyping})
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return cancel
}

func (b *Bot) send(ctx context.Context, chatID int64, text string) {
	for _, part := range split(text, maxMessageLen) {
		if _, err := b.api.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: part}); err != nil {
			b.log.Error("send message", "chat", chatID, "err", err)
			return
		}
	}
}

// split cuts text into Telegram-sized chunks, preferring line breaks.
func split(text string, limit int) []string {
	var parts []string
	r := []rune(text)
	for len(r) > limit {
		cut := limit
		for i := limit; i > limit/2; i-- {
			if r[i] == '\n' {
				cut = i
				break
			}
		}
		parts = append(parts, string(r[:cut]))
		r = []rune(strings.TrimLeft(string(r[cut:]), "\n"))
	}
	return append(parts, string(r))
}

func greeting(name string) string {
	hello := "Привет!"
	if name != "" {
		hello = "Привет, " + name + "!"
	}
	return hello + " " + intro
}

const intro = `Я помогаю вести учёт трат. Пиши обычным текстом:

• «кофе 350» или «такси 1200 вчера» — добавлю трату
• «сколько потратил на еду в этом месяце» — посчитаю
• «покажи траты за неделю» — покажу список
• «последнее было не 350, а 450» — исправлю
• «удали такси» — удалю после подтверждения
• «зови меня Маша» — поменяю имя

Свои траты видишь только ты, а все траты семьи — глава семьи в веб-панели.`

const namingTTL = time.Hour

// naming remembers who was asked for their name after joining.
type naming struct {
	mu    sync.Mutex
	asked map[int64]time.Time
}

func (n *naming) start(id int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.asked == nil {
		n.asked = map[int64]time.Time{}
	}
	n.asked[id] = time.Now().Add(namingTTL)
}

func (n *naming) stop(id int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.asked, id)
}

func (n *naming) waiting(id int64) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	deadline, ok := n.asked[id]
	if ok && time.Now().After(deadline) {
		delete(n.asked, id)
		return false
	}
	return ok
}
