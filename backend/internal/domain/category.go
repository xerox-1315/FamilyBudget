package domain

// Category of an expense. Stored in the database as the code.
type Category string

const (
	CategoryFood          Category = "food"
	CategoryTransport     Category = "transport"
	CategoryHousing       Category = "housing"
	CategoryEntertainment Category = "entertainment"
	CategoryHealth        Category = "health"
	CategoryShopping      Category = "shopping"
	CategoryOther         Category = "other"
)

// Categories lists all categories in display order.
var Categories = []Category{
	CategoryFood,
	CategoryTransport,
	CategoryHousing,
	CategoryEntertainment,
	CategoryHealth,
	CategoryShopping,
	CategoryOther,
}

var categoryNames = map[Category]string{
	CategoryFood:          "Еда",
	CategoryTransport:     "Транспорт",
	CategoryHousing:       "Жильё",
	CategoryEntertainment: "Развлечения",
	CategoryHealth:        "Здоровье",
	CategoryShopping:      "Покупки",
	CategoryOther:         "Другое",
}

func (c Category) Valid() bool {
	_, ok := categoryNames[c]
	return ok
}

// Name returns the Russian display name.
func (c Category) Name() string {
	if n, ok := categoryNames[c]; ok {
		return n
	}
	return string(c)
}
