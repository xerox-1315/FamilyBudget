package service

import (
	"context"
	"errors"
	"testing"

	"familybudget/internal/domain"
	"familybudget/internal/storage/memstore"
)

func TestInviteFlow(t *testing.T) {
	ctx := context.Background()
	svc := New(memstore.New())
	anya := domain.User{TelegramID: 1, FirstName: "Аня"}

	// A stranger is turned away and not stored.
	if ok, err := svc.Access(ctx, anya); ok || err != nil {
		t.Fatalf("stranger access = %v, %v", ok, err)
	}
	if users, _ := svc.Users(ctx); len(users) != 0 {
		t.Fatalf("stranger was stored: %+v", users)
	}

	code, err := svc.InviteCode(ctx)
	if err != nil || code == "" {
		t.Fatalf("invite code: %q %v", code, err)
	}
	if _, err := svc.Join(ctx, anya, "wrong"); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("wrong code: %v", err)
	}
	if already, err := svc.Join(ctx, anya, code); already || err != nil {
		t.Fatalf("join = %v, %v", already, err)
	}
	if already, _ := svc.Join(ctx, anya, code); !already {
		t.Fatal("second join should report membership")
	}
	if ok, _ := svc.Access(ctx, anya); !ok {
		t.Fatal("member has no access")
	}

	// Rotating the code invalidates old links but keeps members.
	newCode, _ := svc.RotateInvite(ctx)
	if _, err := svc.Join(ctx, domain.User{TelegramID: 2}, code); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("old code still works: %v", err)
	}
	if ok, _ := svc.Access(ctx, anya); !ok {
		t.Fatal("rotation removed a member")
	}

	// Excluded members lose access until they join again.
	if err := svc.Exclude(ctx, anya.TelegramID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.Access(ctx, anya); ok {
		t.Fatal("excluded user still has access")
	}
	if _, err := svc.Join(ctx, anya, newCode); err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.Access(ctx, anya); !ok {
		t.Fatal("rejoined user has no access")
	}
}

func TestAccessRefreshesProfileAndKeepsExclusion(t *testing.T) {
	ctx := context.Background()
	svc := New(memstore.New())
	code, _ := svc.InviteCode(ctx)
	if _, err := svc.Join(ctx, domain.User{TelegramID: 7, FirstName: "Петя"}, code); err != nil {
		t.Fatal(err)
	}

	// A member's renamed Telegram profile is picked up on the next message.
	if ok, err := svc.Access(ctx, domain.User{TelegramID: 7, FirstName: "Пётр"}); !ok || err != nil {
		t.Fatalf("access = %v, %v", ok, err)
	}
	users, _ := svc.Users(ctx)
	if len(users) != 1 || users[0].FirstName != "Пётр" || !users[0].IsMember {
		t.Fatalf("users = %+v", users)
	}

	// An excluded user stays excluded no matter how many times they write.
	_ = svc.Exclude(ctx, 7)
	for range 2 {
		if ok, _ := svc.Access(ctx, domain.User{TelegramID: 7}); ok {
			t.Fatal("excluded user regained access")
		}
	}
}
