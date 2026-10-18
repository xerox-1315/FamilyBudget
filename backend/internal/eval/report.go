package eval

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ModelStats aggregates results of one model.
type ModelStats struct {
	Model       string
	Runs        int // runs that reached the model
	Passed      int
	Cases       int
	Stable      int // cases passed in every repeat
	Skipped     int // runs skipped because of the daily quota
	Errors      int // runs ended by an LLM error
	AvgLatency  time.Duration
	AvgCalls    float64
	AvgTokens   float64
	ByCategory  map[string][2]int // category → passed, total runs
	FailedCases []Result          // first failed run of each failing case
}

func (s ModelStats) PassRate() float64 {
	if s.Runs == 0 {
		return 0
	}
	return float64(s.Passed) / float64(s.Runs)
}

// Summarize groups results by model, in the given model order.
func Summarize(results []Result, models []string) []ModelStats {
	var out []ModelStats
	for _, model := range models {
		st := ModelStats{Model: model, ByCategory: map[string][2]int{}}
		perCase := map[string][]Result{}
		var order []string
		var latency time.Duration
		var calls, tokens, measured int
		for _, r := range results {
			if r.Model != model {
				continue
			}
			if r.Skipped {
				st.Skipped++
				continue
			}
			if _, ok := perCase[r.Case.ID]; !ok {
				order = append(order, r.Case.ID)
			}
			perCase[r.Case.ID] = append(perCase[r.Case.ID], r)
			st.Runs++
			cat := st.ByCategory[r.Case.Category]
			cat[1]++
			if r.Passed {
				st.Passed++
				cat[0]++
			}
			st.ByCategory[r.Case.Category] = cat
			if r.Err != nil {
				st.Errors++
			}
			if r.Calls > 0 {
				latency += r.Latency
				calls += r.Calls
				tokens += r.Tokens
				measured++
			}
		}
		if measured > 0 {
			st.AvgLatency = latency / time.Duration(measured)
			st.AvgCalls = float64(calls) / float64(measured)
			st.AvgTokens = float64(tokens) / float64(measured)
		}
		st.Cases = len(order)
		for _, id := range order {
			runs := perCase[id]
			allPassed := true
			for _, r := range runs {
				if !r.Passed {
					allPassed = false
					st.FailedCases = append(st.FailedCases, withFailCount(r, runs))
					break
				}
			}
			if allPassed {
				st.Stable++
			}
		}
		out = append(out, st)
	}
	return out
}

// withFailCount stores how many repeats failed in Repeat as a negative marker-free count.
func withFailCount(r Result, runs []Result) Result {
	failed := 0
	for _, x := range runs {
		if !x.Passed {
			failed++
		}
	}
	r.Repeat = failed
	return r
}

// Markdown renders the full report.
func Markdown(stats []ModelStats, suite *Suite, repeat int, started time.Time, took time.Duration) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("# Eval: качество ИИ-агента\n\n")
	p("Прогон %s, %d %s, повторов на кейс: %d, «сегодня» в кейсах: %s. Длительность: %s.\n\n",
		started.Format("02.01.2006 15:04"), suite.countCases(), plural(suite.countCases(), "кейс", "кейса", "кейсов"),
		repeat, suite.Today, took.Round(time.Second))
	p("Как читать: **пройдено** — доля успешных прогонов; **стабильно** — кейсы, пройденные во всех повторах; ")
	p("время — чистое время модели на одно сообщение без ожидания лимитов провайдера.\n\n")

	p("## Итог по моделям\n\n")
	p("| Модель | Пройдено | Стабильно | Время на сообщение | Запросов к LLM | Токенов на сообщение |\n")
	p("|---|---|---|---|---|---|\n")
	for _, s := range stats {
		p("| `%s` | **%.0f%%** (%d/%d) | %d/%d | %.1f с | %.1f | %.0f |\n",
			s.Model, s.PassRate()*100, s.Passed, s.Runs, s.Stable, s.Cases,
			s.AvgLatency.Seconds(), s.AvgCalls, s.AvgTokens)
	}
	for _, s := range stats {
		if s.Skipped > 0 {
			p("\n> `%s`: %d прогонов пропущено — исчерпан дневной лимит провайдера.\n", s.Model, s.Skipped)
		}
	}

	p("\n## По категориям\n\n| Категория |")
	for _, s := range stats {
		p(" `%s` |", s.Model)
	}
	p("\n|---|")
	for range stats {
		p("---|")
	}
	p("\n")
	for _, cat := range suite.categories() {
		p("| %s |", cat)
		for _, s := range stats {
			c := s.ByCategory[cat]
			if c[1] == 0 {
				p(" — |")
				continue
			}
			mark := ""
			if c[0] < c[1] {
				mark = " ⚠️"
			}
			p(" %d/%d%s |", c[0], c[1], mark)
		}
		p("\n")
	}

	p("\n## Разбор ошибок\n")
	for _, s := range stats {
		p("\n### `%s`\n\n", s.Model)
		if len(s.FailedCases) == 0 {
			p("Все кейсы пройдены во всех повторах.\n")
			continue
		}
		for _, r := range s.FailedCases {
			p("**%s** (%s) — провалов: %d из %d\n\n", r.Case.ID, r.Case.Category, r.Repeat, repeat)
			for i, t := range r.Case.Turns {
				p("> 👤 %s\n", t)
				if i < len(r.Replies) {
					p(">\n> 🤖 %s\n", strings.ReplaceAll(r.Replies[i], "\n", "\n> "))
				}
				p(">\n")
			}
			p("\n")
			for _, f := range r.Failures {
				p("- %s\n", f)
			}
			if len(r.Tools) > 0 {
				p("- вызванные инструменты: %s\n", strings.Join(r.Tools, " → "))
			}
			p("\n")
		}
	}
	return b.String()
}

func (s *Suite) countCases() int { return len(s.Cases) }

// categories lists case categories in the order they first appear.
func (s *Suite) categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range s.Cases {
		if !seen[c.Category] {
			seen[c.Category] = true
			out = append(out, c.Category)
		}
	}
	return out
}

// SortByPassRate orders models from best to worst.
func SortByPassRate(stats []ModelStats) {
	sort.SliceStable(stats, func(i, j int) bool { return stats[i].PassRate() > stats[j].PassRate() })
}

func plural(n int, one, few, many string) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		return few
	default:
		return many
	}
}
