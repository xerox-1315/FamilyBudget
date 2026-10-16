package domain

import (
	"strconv"
	"strings"
)

// Money is an amount in kopecks. Floats are never used for money.
type Money int64

// MaxAmount caps a single expense at 100 million rubles.
const MaxAmount Money = 100_000_000_00

// ParseRubles parses a ruble amount such as "350", "349.90", "1 200,5" into kopecks
// without going through floating point.
func ParseRubles(s string) (Money, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer(" ", "", " ", "", "_", "", ",", ".").Replace(s)
	if s == "" {
		return 0, Invalid("empty amount")
	}
	if strings.HasPrefix(s, "-") {
		return 0, Invalid("amount must be positive")
	}
	s = strings.TrimPrefix(s, "+")

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		intPart = "0"
	}
	if hasFrac {
		// Trailing zeros beyond kopecks are harmless ("350.000"), anything else is not.
		for len(fracPart) > 2 && strings.HasSuffix(fracPart, "0") {
			fracPart = fracPart[:len(fracPart)-1]
		}
		if len(fracPart) > 2 {
			return 0, Invalid("too many decimal places: " + s)
		}
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}
	if !digitsOnly(intPart) || !digitsOnly(fracPart) || len(intPart) > 12 {
		return 0, Invalid("invalid amount: " + s)
	}
	rub, _ := strconv.ParseInt(intPart, 10, 64)
	kop, _ := strconv.ParseInt(fracPart, 10, 64)
	m := Money(rub*100 + kop)
	if m <= 0 {
		return 0, Invalid("amount must be positive")
	}
	if m > MaxAmount {
		return 0, Invalid("amount is too large")
	}
	return m, nil
}

// Rubles formats the amount as a plain decimal string: "350", "349.90".
func (m Money) Rubles() string {
	sign := ""
	v := int64(m)
	if v < 0 {
		sign, v = "-", -v
	}
	rub, kop := v/100, v%100
	if kop == 0 {
		return sign + strconv.FormatInt(rub, 10)
	}
	return sign + strconv.FormatInt(rub, 10) + "." + pad2(kop)
}

// String formats the amount for people: "1 200 ₽", "349,90 ₽".
func (m Money) String() string {
	v := int64(m)
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	rub, kop := v/100, v%100
	s := groupThousands(strconv.FormatInt(rub, 10))
	if kop != 0 {
		s += "," + pad2(kop)
	}
	return sign + s + " ₽"
}

func groupThousands(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func pad2(v int64) string {
	if v < 10 {
		return "0" + strconv.FormatInt(v, 10)
	}
	return strconv.FormatInt(v, 10)
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
