package domain

import "testing"

func TestParseRubles(t *testing.T) {
	cases := []struct {
		in   string
		want Money
		ok   bool
	}{
		{"350", 35000, true},
		{"349.90", 34990, true},
		{"349,9", 34990, true},
		{"0.01", 1, true},
		{"1 200", 120000, true},
		{"1200.000", 120000, true},
		{"0.1", 10, true},
		{".5", 50, true},
		{"0", 0, false},
		{"-5", 0, false},
		{"1.234", 0, false},
		{"abc", 0, false},
		{"", 0, false},
		{"1e5", 0, false},
		{"999999999999999", 0, false},
	}
	for _, c := range cases {
		got, err := ParseRubles(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("ParseRubles(%q) = %d, %v; want %d, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestParseName(t *testing.T) {
	ok := map[string]string{
		"маша":            "Маша",
		"  Анна   Мария ": "Анна Мария",
		"Жан-Поль":        "Жан-Поль",
		"д'Артаньян":      "Д'Артаньян",
		"Mom":             "Mom",
	}
	for in, want := range ok {
		if got, err := ParseName(in); err != nil || got != want {
			t.Errorf("ParseName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "кофе 350", "---", "маша😀", "@masha", "оченьдлинноеимякотороеявнобольшесорокасимволов"} {
		if got, err := ParseName(in); err == nil {
			t.Errorf("ParseName(%q) = %q, want error", in, got)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		u    User
		want string
	}{
		{User{TelegramID: 1, FirstName: "Danil", Name: "Папа"}, "Папа"},
		{User{TelegramID: 1, FirstName: "Danil", LastName: "K"}, "Danil K"},
		{User{TelegramID: 1, Username: "danil"}, "@danil"},
		{User{TelegramID: 42}, "ID 42"},
	}
	for _, c := range cases {
		if got := c.u.DisplayName(); got != c.want {
			t.Errorf("DisplayName(%+v) = %q, want %q", c.u, got, c.want)
		}
	}
}

func TestMoneyFormat(t *testing.T) {
	cases := map[Money]string{
		35000:     "350 ₽",
		34990:     "349,90 ₽",
		120000:    "1 200 ₽",
		123456789: "1 234 567,89 ₽",
		5:         "0,05 ₽",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("Money(%d).String() = %q, want %q", m, got, want)
		}
	}
	if got := Money(34990).Rubles(); got != "349.90" {
		t.Errorf("Rubles() = %q", got)
	}
}
