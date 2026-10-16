package parser

import (
	"testing"
	"time"

	"familybudget/internal/domain"
)

func TestParse(t *testing.T) {
	today := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	d := func(m time.Month, day int) time.Time { return time.Date(2026, m, day, 0, 0, 0, 0, time.UTC) }

	cases := []struct {
		in       string
		amount   domain.Money
		category domain.Category
		date     time.Time
		comment  string
	}{
		{"кофе 350", 35000, domain.CategoryFood, today, "кофе"},
		{"Такси 1200 вчера", 120000, domain.CategoryTransport, d(10, 15), "такси"},
		{"350 кофе", 35000, domain.CategoryFood, today, "кофе"},
		{"продукты в магните 2500,50 руб", 250050, domain.CategoryFood, today, "продукты в магните"},
		{"аптека 780р позавчера", 78000, domain.CategoryHealth, d(10, 14), "аптека"},
		{"куртка 7.5к 12.10", 750000, domain.CategoryShopping, d(10, 12), "куртка"},
		{"кино 900 25.12", 90000, domain.CategoryEntertainment, time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC), "кино"},
		{"коммуналка 6400", 640000, domain.CategoryHousing, today, "коммуналка"},
		{"что-то странное 100", 10000, domain.CategoryOther, today, "что-то странное"},
	}
	for _, c := range cases {
		got, ok := Parse(c.in, today)
		if !ok {
			t.Errorf("Parse(%q) failed", c.in)
			continue
		}
		if got.Amount != c.amount || got.Category != c.category || !got.SpentAt.Equal(c.date) || got.Comment != c.comment {
			t.Errorf("Parse(%q) = %+v", c.in, got)
		}
	}
}

func TestParseRejects(t *testing.T) {
	today := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, in := range []string{
		"кофе",
		"350",
		"сколько потратил на еду",
		"последнее было не 350, а 450",
		"удали такси 1200",
		"покажи траты за 7 дней",
		"кофе 350 и такси 500",
		"кофе 31.02 300",
	} {
		if got, ok := Parse(in, today); ok {
			t.Errorf("Parse(%q) = %+v, want rejection", in, got)
		}
	}
}
