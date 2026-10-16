package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"familybudget/internal/domain"
)

const inviteKey = "invite_code"

// Access reports whether a Telegram user may use the bot and refreshes their profile.
// Only members, who joined through the invite link, are allowed; strangers are not stored.
func (s *Expenses) Access(ctx context.Context, u domain.User) (bool, error) {
	existing, err := s.store.GetUser(ctx, u.TelegramID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !existing.IsMember {
		return false, nil
	}
	return true, s.store.UpsertUser(ctx, u)
}

// Join adds the user to the family if the invite code is valid.
// It reports whether the user was already a member.
func (s *Expenses) Join(ctx context.Context, u domain.User, code string) (bool, error) {
	current, err := s.InviteCode(ctx)
	if err != nil {
		return false, err
	}
	code = strings.TrimSpace(code)
	if subtle.ConstantTimeCompare([]byte(code), []byte(current)) != 1 {
		return false, domain.ErrInvalidInvite
	}
	existing, err := s.store.GetUser(ctx, u.TelegramID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}
	already := err == nil && existing.IsMember
	if err := s.store.UpsertUser(ctx, u); err != nil {
		return false, err
	}
	if !already {
		if err := s.store.SetMember(ctx, u.TelegramID, true); err != nil {
			return false, err
		}
	}
	return already, nil
}

// InviteCode returns the current invite code, creating one on first use.
func (s *Expenses) InviteCode(ctx context.Context) (string, error) {
	code, err := s.store.Setting(ctx, inviteKey)
	if err != nil || code != "" {
		return code, err
	}
	return s.store.SetSettingIfEmpty(ctx, inviteKey, newInviteCode())
}

// RotateInvite replaces the invite code, so previously shared links stop working.
func (s *Expenses) RotateInvite(ctx context.Context) (string, error) {
	code := newInviteCode()
	return code, s.store.SetSetting(ctx, inviteKey, code)
}

func (s *Expenses) User(ctx context.Context, id int64) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}

// Rename sets the name the member is shown under; an empty name returns to the Telegram name.
func (s *Expenses) Rename(ctx context.Context, id int64, name string) (domain.User, error) {
	if strings.TrimSpace(name) != "" {
		var err error
		if name, err = domain.ParseName(name); err != nil {
			return domain.User{}, err
		}
	} else {
		name = ""
	}
	if err := s.store.SetName(ctx, id, name); err != nil {
		return domain.User{}, err
	}
	return s.store.GetUser(ctx, id)
}

// Exclude removes the user from the family. Their expenses are kept.
func (s *Expenses) Exclude(ctx context.Context, id int64) error {
	return s.store.SetMember(ctx, id, false)
}

// newInviteCode returns a random code usable as a Telegram deep-link parameter.
func newInviteCode() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
