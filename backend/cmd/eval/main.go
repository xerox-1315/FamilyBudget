// Command eval measures the agent's quality on scripted dialogues against real LLMs.
//
//	go run ./cmd/eval -models openai/gpt-oss-120b,openai/gpt-oss-20b -repeat 3
//
// It needs LLM_BASE_URL and LLM_API_KEY (like the server) but no database: each case runs
// on a fresh in-memory store with a fixed "today", so results do not depend on the date.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/sashabaranov/go-openai"

	"familybudget/internal/eval"
)

func main() {
	var (
		casesPath = flag.String("cases", "eval/cases.yaml", "cases file")
		models    = flag.String("models", "openai/gpt-oss-120b,openai/gpt-oss-20b,qwen/qwen3.8-27b", "comma-separated models")
		repeat    = flag.Int("repeat", 1, "runs per case")
		only      = flag.String("only", "", "run only cases whose id or category contains this")
		out       = flag.String("out", "", "optional path for a detailed Markdown report with failed dialogues")
		tz        = flag.String("tz", "Europe/Moscow", "timezone of the agent")
	)
	flag.Parse()

	if err := run(*casesPath, strings.Split(*models, ","), *repeat, *only, *out, *tz); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

func run(casesPath string, models []string, repeat int, only, out, tz string) error {
	suite, err := eval.Load(casesPath)
	if err != nil {
		return err
	}
	key := os.Getenv("LLM_API_KEY")
	if key == "" {
		return fmt.Errorf("LLM_API_KEY is not set")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.groq.com/openai/v1"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}
	cfg := openai.DefaultConfig(key)
	cfg.BaseURL = baseURL
	cfg.HTTPClient = &http.Client{Timeout: 90 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	total := 0
	for i := range suite.Cases {
		if matches(&suite.Cases[i], only) {
			total++
		}
	}
	fmt.Printf("%d cases × %d repeats × %d models\n", total, repeat, len(models))

	var mu sync.Mutex
	done := map[string]int{}
	started := time.Now()
	results := eval.Run(ctx, eval.Options{
		Suite:  suite,
		Client: openai.NewClientWithConfig(cfg),
		Models: models,
		Repeat: repeat,
		Filter: func(c *eval.Case) bool { return matches(c, only) },
		Loc:    loc,
		OnDone: func(r eval.Result) {
			mu.Lock()
			defer mu.Unlock()
			done[r.Model]++
			status := "PASS"
			switch {
			case r.Skipped:
				status = "SKIP"
			case !r.Passed:
				status = "FAIL"
			}
			fmt.Printf("[%-22s %3d/%d] %s %-28s %5.1fs", r.Model, done[r.Model], total*repeat, status, r.Case.ID, r.Latency.Seconds())
			if !r.Passed && !r.Skipped && len(r.Failures) > 0 {
				fmt.Printf("  %s", r.Failures[0])
			}
			fmt.Println()
		},
	})
	took := time.Since(started)

	stats := eval.Summarize(results, models)
	fmt.Println()
	for _, s := range stats {
		fmt.Printf("%-24s passed %3.0f%% (%d/%d), stable %d/%d, %.1fs/msg, %.1f calls/msg, %.0f tokens/msg\n",
			s.Model, s.PassRate()*100, s.Passed, s.Runs, s.Stable, s.Cases, s.AvgLatency.Seconds(), s.AvgCalls, s.AvgTokens)
	}
	if out != "" {
		eval.SortByPassRate(stats)
		if err := os.WriteFile(out, []byte(eval.Markdown(stats, suite, repeat, started, took)), 0o644); err != nil {
			return err
		}
		fmt.Println("report:", out)
	}
	return nil
}

func matches(c *eval.Case, only string) bool {
	return only == "" || strings.Contains(c.ID, only) || c.Category == only
}
