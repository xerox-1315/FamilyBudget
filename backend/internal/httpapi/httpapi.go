// Package httpapi exposes the REST API used by the web panel.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"familybudget/internal/domain"
	"familybudget/internal/service"
)

type API struct {
	svc         *service.Expenses
	ping        func(context.Context) error
	botUsername string // empty when the bot is not running
	log         *slog.Logger
}

func New(svc *service.Expenses, ping func(context.Context) error, botUsername string, log *slog.Logger) *API {
	return &API{svc: svc, ping: ping, botUsername: botUsername, log: log}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/categories", a.categories)
	mux.HandleFunc("GET /api/users", a.users)
	mux.HandleFunc("PATCH /api/users/{id}", a.renameUser)
	mux.HandleFunc("DELETE /api/users/{id}/membership", a.excludeUser)
	mux.HandleFunc("GET /api/invite", a.invite)
	mux.HandleFunc("POST /api/invite/rotate", a.rotateInvite)
	mux.HandleFunc("GET /api/expenses", a.listExpenses)
	mux.HandleFunc("GET /api/expenses/summary", a.summary)
	mux.HandleFunc("POST /api/expenses", a.createExpense)
	mux.HandleFunc("GET /api/expenses/{id}", a.getExpense)
	mux.HandleFunc("PUT /api/expenses/{id}", a.updateExpense)
	mux.HandleFunc("DELETE /api/expenses/{id}", a.deleteExpense)
	return a.middleware(mux)
}

// --- DTOs ---

type expenseDTO struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	UserName     string    `json:"user_name"`
	Amount       int64     `json:"amount"`
	Category     string    `json:"category"`
	CategoryName string    `json:"category_name"`
	SpentAt      string    `json:"spent_at"`
	Comment      string    `json:"comment"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type userDTO struct {
	TelegramID   int64      `json:"telegram_id"`
	Name         string     `json:"name"`        // what to show: chosen name or the Telegram one
	CustomName   string     `json:"custom_name"` // chosen name, empty if not set
	TelegramName string     `json:"telegram_name"`
	Username     string     `json:"username"`
	IsMember     bool       `json:"is_member"`
	JoinedAt     *time.Time `json:"joined_at"`
}

func toUserDTO(u domain.User) userDTO {
	return userDTO{
		TelegramID: u.TelegramID, Name: u.DisplayName(), CustomName: u.Name, TelegramName: u.TelegramName(),
		Username: u.Username, IsMember: u.IsMember, JoinedAt: u.JoinedAt,
	}
}

type inviteDTO struct {
	Code        string `json:"code"`
	Link        string `json:"link"`         // empty when the bot is not configured
	BotUsername string `json:"bot_username"` // without "@"
}

type categoryDTO struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type listResponse struct {
	Items       []expenseDTO `json:"items"`
	Total       int          `json:"total"`
	TotalAmount int64        `json:"total_amount"`
}

type categoryTotalDTO struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Amount   int64  `json:"amount"`
	Count    int    `json:"count"`
}

type summaryResponse struct {
	TotalAmount int64              `json:"total_amount"`
	Count       int                `json:"count"`
	ByCategory  []categoryTotalDTO `json:"by_category"`
}

type expenseInput struct {
	UserID   int64   `json:"user_id"`
	Amount   int64   `json:"amount"`
	Category string  `json:"category"`
	SpentAt  string  `json:"spent_at"`
	Comment  *string `json:"comment"`
}

// --- handlers ---

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) categories(w http.ResponseWriter, _ *http.Request) {
	out := make([]categoryDTO, 0, len(domain.Categories))
	for _, c := range domain.Categories {
		out = append(out, categoryDTO{Code: string(c), Name: c.Name()})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) users(w http.ResponseWriter, r *http.Request) {
	users, err := a.svc.Users(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, toUserDTO(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) renameUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	u, err := a.svc.Rename(r.Context(), id, in.Name)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserDTO(u))
}

func (a *API) excludeUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.Exclude(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) invite(w http.ResponseWriter, r *http.Request) {
	code, err := a.svc.InviteCode(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.inviteDTO(code))
}

func (a *API) rotateInvite(w http.ResponseWriter, r *http.Request) {
	code, err := a.svc.RotateInvite(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.inviteDTO(code))
}

func (a *API) inviteDTO(code string) inviteDTO {
	dto := inviteDTO{Code: code, BotUsername: a.botUsername}
	if a.botUsername != "" {
		dto.Link = "https://t.me/" + a.botUsername + "?start=" + code
	}
	return dto
}

func (a *API) listExpenses(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	items, err := a.svc.List(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sum, err := a.svc.Summarize(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	names, err := a.userNames(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp := listResponse{Items: make([]expenseDTO, 0, len(items)), Total: sum.Count, TotalAmount: int64(sum.Total)}
	for _, e := range items {
		resp.Items = append(resp.Items, toDTO(e, names))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) summary(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sum, err := a.svc.Summarize(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp := summaryResponse{TotalAmount: int64(sum.Total), Count: sum.Count, ByCategory: []categoryTotalDTO{}}
	for _, t := range sum.ByCategory {
		resp.ByCategory = append(resp.ByCategory, categoryTotalDTO{
			Category: string(t.Category), Name: t.Category.Name(), Amount: int64(t.Amount), Count: t.Count,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) createExpense(w http.ResponseWriter, r *http.Request) {
	var in expenseInput
	if err := decode(w, r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	spent, err := domain.ParseDate(in.SpentAt)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	created, err := a.svc.Create(r.Context(), []domain.NewExpense{{
		UserID:   in.UserID,
		Amount:   domain.Money(in.Amount),
		Category: domain.Category(in.Category),
		SpentAt:  spent,
		Comment:  deref(in.Comment),
	}})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.respondExpense(w, r, http.StatusCreated, created[0])
}

func (a *API) getExpense(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	e, err := a.svc.Get(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.respondExpense(w, r, http.StatusOK, e)
}

func (a *API) updateExpense(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in expenseInput
	if err := decode(w, r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	spent, err := domain.ParseDate(in.SpentAt)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	amount := domain.Money(in.Amount)
	cat := domain.Category(in.Category)
	comment := deref(in.Comment)
	e, err := a.svc.Update(r.Context(), id, nil, domain.ExpensePatch{
		Amount: &amount, Category: &cat, SpentAt: &spent, Comment: &comment,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.respondExpense(w, r, http.StatusOK, e)
}

func (a *API) deleteExpense(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	deleted, err := a.svc.Delete(r.Context(), []int64{id}, nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(deleted) == 0 {
		a.fail(w, r, domain.ErrNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func (a *API) respondExpense(w http.ResponseWriter, r *http.Request, status int, e domain.Expense) {
	names, err := a.userNames(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, status, toDTO(e, names))
}

func (a *API) userNames(ctx context.Context) (map[int64]string, error) {
	users, err := a.svc.Users(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(users))
	for _, u := range users {
		names[u.TelegramID] = u.DisplayName()
	}
	return names, nil
}

func toDTO(e domain.Expense, names map[int64]string) expenseDTO {
	name := names[e.UserID]
	if name == "" {
		name = "ID " + strconv.FormatInt(e.UserID, 10)
	}
	return expenseDTO{
		ID: e.ID, UserID: e.UserID, UserName: name,
		Amount: int64(e.Amount), Category: string(e.Category), CategoryName: e.Category.Name(),
		SpentAt: e.SpentAt.Format(domain.DateLayout), Comment: e.Comment,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
}

func parseFilter(r *http.Request) (domain.ExpenseFilter, error) {
	q := r.URL.Query()
	var f domain.ExpenseFilter
	for _, v := range splitList(q["user_id"]) {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return f, domain.Invalid("invalid user_id: " + v)
		}
		f.UserIDs = append(f.UserIDs, id)
	}
	for _, v := range splitList(q["category"]) {
		f.Categories = append(f.Categories, domain.Category(v))
	}
	for key, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(key); v != "" {
			d, err := domain.ParseDate(v)
			if err != nil {
				return f, err
			}
			*dst = &d
		}
	}
	for key, dst := range map[string]*int{"limit": &f.Limit, "offset": &f.Offset} {
		if v := q.Get(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return f, domain.Invalid("invalid " + key)
			}
			*dst = n
		}
	}
	f.Query = q.Get("q")
	return f, nil
}

func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Invalid("invalid id")
	}
	return id, nil
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.Invalid("invalid JSON body: " + err.Error())
	}
	return nil
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var verr *domain.ValidationError
	switch {
	case errors.As(err, &verr):
		writeError(w, http.StatusBadRequest, verr.Msg)
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		a.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if p := recover(); p != nil {
				a.log.Error("panic", "path", r.URL.Path, "panic", p)
				writeError(rec, http.StatusInternalServerError, "internal error")
			}
			if r.URL.Path != "/healthz" {
				a.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "took", time.Since(start))
			}
		}()
		next.ServeHTTP(rec, r)
	})
}
