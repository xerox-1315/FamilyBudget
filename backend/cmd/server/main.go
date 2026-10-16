package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // timezones work in minimal containers

	"github.com/sashabaranov/go-openai"

	"familybudget/internal/agent"
	"familybudget/internal/bot"
	"familybudget/internal/config"
	"familybudget/internal/httpapi"
	"familybudget/internal/service"
	"familybudget/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info("database ready")

	svc := service.New(db)

	opts := agent.Options{Models: cfg.LLM.Models(), Location: cfg.Location, Logger: log}
	if cfg.LLM.Enabled() {
		oc := openai.DefaultConfig(cfg.LLM.APIKey)
		oc.BaseURL = cfg.LLM.BaseURL
		oc.HTTPClient = &http.Client{Timeout: cfg.LLM.Timeout}
		opts.LLM = openai.NewClientWithConfig(oc)
		log.Info("llm enabled", "base_url", cfg.LLM.BaseURL, "models", cfg.LLM.Models())
	} else {
		log.Warn("LLM_API_KEY is not set: the bot works in fallback mode (simple expenses only)")
	}
	ag := agent.New(svc, opts)

	var wg sync.WaitGroup

	botUsername := ""
	if cfg.TelegramToken != "" {
		tg, err := bot.New(cfg.TelegramToken, ag, svc, log)
		if err != nil {
			return err
		}
		botUsername = tg.Username()
		wg.Add(1)
		go func() {
			defer wg.Done()
			tg.Run(ctx)
		}()
	} else {
		log.Warn("TELEGRAM_BOT_TOKEN is not set: only the REST API is running")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ag.Prune()
			}
		}
	}()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(svc, db.Ping, botUsername, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("http server started", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		stop()
		wg.Wait()
		return err
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
	return nil
}
