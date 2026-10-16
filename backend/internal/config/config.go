// Package config loads the service configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	LogLevel    slog.Level

	TelegramToken string

	LLM LLMConfig

	Location *time.Location
}

type LLMConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	FallbackModels []string // tried in order when the main model is rate-limited
	Timeout        time.Duration
}

// Enabled reports whether the LLM is configured.
func (c LLMConfig) Enabled() bool { return c.APIKey != "" && c.Model != "" }

// Models returns the main model followed by the fallbacks.
func (c LLMConfig) Models() []string {
	out := []string{c.Model}
	for _, m := range c.FallbackModels {
		if m != c.Model {
			out = append(out, m)
		}
	}
	return out
}

func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:      env("HTTP_ADDR", ":8081"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		TelegramToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		LLM: LLMConfig{
			BaseURL: env("LLM_BASE_URL", "https://api.groq.com/openai/v1"),
			APIKey:  strings.TrimSpace(os.Getenv("LLM_API_KEY")),
			Model:   env("LLM_MODEL", "openai/gpt-oss-120b"),
			FallbackModels: strings.FieldsFunc(os.Getenv("LLM_FALLBACK_MODELS"), func(r rune) bool {
				return r == ',' || r == ' '
			}),
		},
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(env("LOG_LEVEL", "info"))); err != nil {
		return nil, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	timeout, err := time.ParseDuration(env("LLM_TIMEOUT", "60s"))
	if err != nil {
		return nil, fmt.Errorf("LLM_TIMEOUT: %w", err)
	}
	cfg.LLM.Timeout = timeout

	loc, err := time.LoadLocation(env("APP_TIMEZONE", "Europe/Moscow"))
	if err != nil {
		return nil, fmt.Errorf("APP_TIMEZONE: %w", err)
	}
	cfg.Location = loc
	return cfg, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
