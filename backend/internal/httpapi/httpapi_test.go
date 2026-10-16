package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"familybudget/internal/domain"
	"familybudget/internal/service"
	"familybudget/internal/storage/memstore"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	svc := service.New(memstore.New())
	for _, u := range []domain.User{{TelegramID: 1, FirstName: "Аня"}, {TelegramID: 2, Username: "petya"}} {
		if err := svc.TouchUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	api := New(svc, func(context.Context) error { return nil }, "family_bot", slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func TestExpenseCRUD(t *testing.T) {
	srv := newServer(t)

	var created expenseDTO
	code := do(t, srv, "POST", "/api/expenses",
		`{"user_id":1,"amount":35000,"category":"food","spent_at":"2026-10-15","comment":"кофе"}`, &created)
	if code != http.StatusCreated || created.ID == 0 || created.UserName != "Аня" || created.CategoryName != "Еда" {
		t.Fatalf("create: %d %+v", code, created)
	}
	do(t, srv, "POST", "/api/expenses", `{"user_id":2,"amount":120000,"category":"transport","spent_at":"2026-10-16"}`, nil)

	var list listResponse
	do(t, srv, "GET", "/api/expenses?user_id=1", "", &list)
	if list.Total != 1 || list.TotalAmount != 35000 || len(list.Items) != 1 {
		t.Fatalf("filtered list: %+v", list)
	}
	do(t, srv, "GET", "/api/expenses?from=2026-10-16&to=2026-10-16", "", &list)
	if list.Total != 1 || list.Items[0].UserName != "@petya" {
		t.Fatalf("date filter: %+v", list)
	}

	var updated expenseDTO
	code = do(t, srv, "PUT", "/api/expenses/"+itoa(created.ID),
		`{"amount":45000,"category":"food","spent_at":"2026-10-15","comment":"кофе с собой"}`, &updated)
	if code != http.StatusOK || updated.Amount != 45000 || updated.Comment != "кофе с собой" {
		t.Fatalf("update: %d %+v", code, updated)
	}

	var sum summaryResponse
	do(t, srv, "GET", "/api/expenses/summary", "", &sum)
	if sum.TotalAmount != 165000 || sum.Count != 2 || len(sum.ByCategory) != 2 {
		t.Fatalf("summary: %+v", sum)
	}

	if code := do(t, srv, "DELETE", "/api/expenses/"+itoa(created.ID), "", nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code := do(t, srv, "GET", "/api/expenses/"+itoa(created.ID), "", nil); code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", code)
	}
}

func TestInviteAndMembership(t *testing.T) {
	srv := newServer(t)

	var inv inviteDTO
	do(t, srv, "GET", "/api/invite", "", &inv)
	if len(inv.Code) < 16 || inv.Link != "https://t.me/family_bot?start="+inv.Code {
		t.Fatalf("invite = %+v", inv)
	}
	var again inviteDTO
	do(t, srv, "GET", "/api/invite", "", &again)
	if again.Code != inv.Code {
		t.Fatal("invite code changes between reads")
	}
	var rotated inviteDTO
	do(t, srv, "POST", "/api/invite/rotate", "", &rotated)
	if rotated.Code == inv.Code || rotated.Code == "" {
		t.Fatalf("rotate did not change the code: %+v", rotated)
	}

	var renamed userDTO
	if code := do(t, srv, "PATCH", "/api/users/1", `{"name":"мама"}`, &renamed); code != http.StatusOK ||
		renamed.Name != "Мама" || renamed.CustomName != "Мама" || renamed.TelegramName != "Аня" {
		t.Fatalf("rename: %d %+v", code, renamed)
	}
	if code := do(t, srv, "PATCH", "/api/users/1", `{"name":"мама2"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("invalid name: %d", code)
	}
	if code := do(t, srv, "PATCH", "/api/users/1", `{"name":""}`, &renamed); code != http.StatusOK || renamed.Name != "Аня" {
		t.Fatalf("reset name: %d %+v", code, renamed)
	}

	if code := do(t, srv, "DELETE", "/api/users/1/membership", "", nil); code != http.StatusNoContent {
		t.Fatalf("exclude: %d", code)
	}
	if code := do(t, srv, "DELETE", "/api/users/42/membership", "", nil); code != http.StatusNotFound {
		t.Fatalf("exclude unknown: %d", code)
	}
}

func TestValidation(t *testing.T) {
	srv := newServer(t)
	for _, body := range []string{
		`{"user_id":1,"amount":0,"category":"food","spent_at":"2026-10-15"}`,
		`{"user_id":1,"amount":100,"category":"cars","spent_at":"2026-10-15"}`,
		`{"user_id":1,"amount":100,"category":"food","spent_at":"15.10.2026"}`,
		`{"user_id":99,"amount":100,"category":"food","spent_at":"2026-10-15"}`,
		`{"user_id":1,"amount":100,"category":"food","spent_at":"2026-10-15","extra":1}`,
		`not json`,
	} {
		var e map[string]string
		if code := do(t, srv, "POST", "/api/expenses", body, &e); code != http.StatusBadRequest || e["error"] == "" {
			t.Errorf("%s: got %d %v", body, code, e)
		}
	}
	if code := do(t, srv, "GET", "/api/expenses?category=cars", "", nil); code != http.StatusBadRequest {
		t.Errorf("bad category filter: %d", code)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
