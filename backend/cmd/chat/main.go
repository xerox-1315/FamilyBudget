// Command chat talks to the expense agent from the terminal, bypassing Telegram.
// It uses the same configuration as the server, so it is handy for checking the LLM setup:
//
//	echo "кофе 350" | go run ./cmd/chat -user 123
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	_ "time/tzdata"

	"github.com/sashabaranov/go-openai"

	"familybudget/internal/agent"
	"familybudget/internal/config"
	"familybudget/internal/domain"
	"familybudget/internal/service"
	"familybudget/internal/storage"
)

func main() {
	userID := flag.Int64("user", 1, "Telegram ID to act as")
	name := flag.String("name", "Тест", "user's first name")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx := context.Background()

	db, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	svc := service.New(db)
	if err := svc.TouchUser(ctx, domain.User{TelegramID: *userID, FirstName: *name}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	opts := agent.Options{Models: cfg.LLM.Models(), Location: cfg.Location, Logger: log}
	if cfg.LLM.Enabled() {
		oc := openai.DefaultConfig(cfg.LLM.APIKey)
		oc.BaseURL = cfg.LLM.BaseURL
		oc.HTTPClient = &http.Client{Timeout: cfg.LLM.Timeout}
		opts.LLM = openai.NewClientWithConfig(oc)
	}
	ag := agent.New(svc, opts)

	in := bufio.NewScanner(os.Stdin)
	for fmt.Print("> "); in.Scan(); fmt.Print("> ") {
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		fmt.Printf("%s\n%s\n\n", text, ag.Handle(ctx, *userID, text))
	}
}
