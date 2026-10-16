// Package parser is a deterministic fallback for simple expense messages such as
// "кофе 350" or "такси 1200 вчера". It is used when the LLM is not configured or unavailable.
package parser

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"familybudget/internal/domain"
)

type Result struct {
	Amount   domain.Money
	Category domain.Category
	SpentAt  time.Time
	Comment  string
}

var (
	amountRe = regexp.MustCompile(`^(\d+(?:[.,]\d{1,2})?)(к|k|тыс\.?)?(₽|р\.?|руб\.?|рублей|рубля|рубль)?$`)
	dateRe   = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})(?:\.(\d{2}|\d{4}))?$`)
	// Words that mean the message is a question or a command, not a new expense.
	intentStems = []string{
		"сколько", "покаж", "показа", "удали", "удалить", "убери", "измени", "изменить",
		"исправ", "поменя", "последн", "итог", "отчет", "отчёт", "верни", "отмен", "статистик",
		"трат", "расход", "помог", "помощ", "привет",
	}
	intentWords   = map[string]bool{"какие": true, "какая": true, "какой": true, "что": true, "где": true, "когда": true}
	currencyWords = map[string]bool{"р": true, "р.": true, "руб": true, "руб.": true, "рублей": true, "рубля": true, "рубль": true, "₽": true}
)

// Parse extracts a single expense from text. today is the current date in the app timezone.
func Parse(text string, today time.Time) (Result, bool) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if len(tokens) < 2 || len(tokens) > 8 {
		return Result{}, false
	}

	res := Result{SpentAt: domain.DateOnly(today)}
	var words []string
	amountFound, dateFound := false, false

	for _, tok := range tokens {
		tok = strings.Trim(tok, ",;!")
		if tok == "" || currencyWords[tok] {
			continue
		}
		if d, ok := parseDateWord(tok, today); ok {
			if dateFound {
				return Result{}, false
			}
			res.SpentAt, dateFound = d, true
			continue
		}
		if m := amountRe.FindStringSubmatch(tok); m != nil {
			if amountFound {
				return Result{}, false // several numbers — too ambiguous for the fallback
			}
			amt, err := domain.ParseRubles(m[1])
			if err != nil {
				return Result{}, false
			}
			if m[2] != "" {
				amt *= 1000
				if amt > domain.MaxAmount {
					return Result{}, false
				}
			}
			res.Amount, amountFound = amt, true
			continue
		}
		words = append(words, tok)
	}

	if !amountFound || len(words) == 0 || len(words) > 5 {
		return Result{}, false
	}
	for _, w := range words {
		if !hasLetter(w) || isIntent(w) {
			return Result{}, false
		}
	}

	res.Comment = strings.Join(words, " ")
	res.Category = Categorize(res.Comment)
	return res, true
}

// LooksLikeRequest reports whether text contains words of a question or a command
// ("покажи", "сколько", "удали"…), as opposed to a plain name or item.
func LooksLikeRequest(text string) bool {
	for _, w := range strings.Fields(strings.ToLower(text)) {
		if isIntent(strings.Trim(w, ",.;!?")) {
			return true
		}
	}
	return false
}

func isIntent(w string) bool {
	if intentWords[w] {
		return true
	}
	for _, stem := range intentStems {
		if strings.HasPrefix(w, stem) {
			return true
		}
	}
	return false
}

func parseDateWord(tok string, today time.Time) (time.Time, bool) {
	today = domain.DateOnly(today)
	switch tok {
	case "сегодня":
		return today, true
	case "вчера":
		return today.AddDate(0, 0, -1), true
	case "позавчера":
		return today.AddDate(0, 0, -2), true
	}
	m := dateRe.FindStringSubmatch(tok)
	if m == nil {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	year := today.Year()
	if m[3] != "" {
		year, _ = strconv.Atoi(m[3])
		if year < 100 {
			year += 2000
		}
	}
	d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if d.Day() != day || int(d.Month()) != month {
		return time.Time{}, false // e.g. 31.02
	}
	// "25.12" written in January most likely means last December.
	if m[3] == "" && d.After(today) {
		d = d.AddDate(-1, 0, 0)
	}
	return d, true
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// keywords maps word stems to categories. Order matters only for readability.
var keywords = []struct {
	cat   domain.Category
	stems []string
}{
	{domain.CategoryFood, []string{
		"кофе", "капуч", "латте", "чай", "еда", "продукт", "магнит", "пятерочк", "пятёрочк", "перекрест",
		"лента", "ашан", "вкусвилл", "обед", "ужин", "завтрак", "кафе", "ресторан", "столов", "пицц",
		"суши", "роллы", "бургер", "шаурм", "макдон", "kfc", "доставк", "хлеб", "молоко", "мясо",
		"овощ", "фрукт", "сладк", "торт", "булк", "перекус", "снек", "вода", "сок", "пиво", "вино",
	}},
	{domain.CategoryTransport, []string{
		"такси", "метро", "автобус", "трамва", "троллейб", "электрич", "поезд", "билет на", "бензин",
		"топлив", "заправк", "азс", "парковк", "каршер", "самокат", "проезд", "транспорт", "убер",
		"яндекс го", "мойк", "шиномонт", "автомо", "самол", "авиа",
	}},
	{domain.CategoryHousing, []string{
		"аренд", "квартплат", "коммунал", "жкх", "ипотек", "свет", "электричеств", "газ", "отоплен",
		"интернет", "ремонт", "сантех", "жиль", "квартир", "домофон",
	}},
	{domain.CategoryEntertainment, []string{
		"кино", "театр", "концерт", "музей", "игра", "игры", "игру", "подписк", "нетфликс", "netflix", "spotify",
		"боулинг", "бар", "клуб", "квест", "развлеч", "караоке", "выставк", "парк", "отпуск", "отель",
	}},
	{domain.CategoryHealth, []string{
		"аптек", "лекарств", "таблет", "врач", "доктор", "стомат", "зуб", "анализ", "клиник",
		"больниц", "витамин", "массаж", "здоров", "спортзал", "фитнес", "бассейн", "окулист", "очки",
	}},
	{domain.CategoryShopping, []string{
		"одежд", "обув", "куртк", "джинс", "футболк", "кроссовк", "платье", "техник", "телефон",
		"ноутбук", "наушник", "озон", "ozon", "вайлдберриз", "wildberries", "вб", "маркетплейс",
		"подарок", "подарк", "косметик", "бытов", "мебел", "икеа", "посуд", "игрушк", "книг",
	}},
}

// Categorize guesses the category of an expense by its description.
func Categorize(comment string) domain.Category {
	s := strings.ToLower(comment)
	words := strings.Fields(s)
	for _, k := range keywords {
		for _, stem := range k.stems {
			if strings.Contains(stem, " ") {
				if strings.Contains(s, stem) {
					return k.cat
				}
				continue
			}
			for _, w := range words {
				if strings.HasPrefix(w, stem) {
					return k.cat
				}
			}
		}
	}
	return domain.CategoryOther
}
