package parser

import "encoding/json"

// Task — один отслеживаемый товар и поисковый запрос, по которому его ищут
type Task struct {
	SKU   int64
	Query string
}

// Result — ответ поиска Wildberries по одному запросу
type Result struct {
	Query string          `json:"query"`
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	URL   string          `json:"url"`
	Data  json.RawMessage `json:"data"`
}

// searchResponse — нужные поля ответа поискового API Wildberries
type searchResponse struct {
	Total    int       `json:"total"`
	Products []Product `json:"products"`
}

// Product — товар из выдачи
type Product struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Brand         string  `json:"brand"`
	Supplier      string  `json:"supplier"`
	ReviewRating  float64 `json:"reviewRating"`
	Feedbacks     int     `json:"feedbacks"`
	TotalQuantity int     `json:"totalQuantity"`
	Sizes         []struct {
		Price *struct {
			Basic   int64 `json:"basic"`
			Product int64 `json:"product"`
		} `json:"price"`
	} `json:"sizes"`
}

// Prices возвращает цену до скидки и цену продажи в копейках по самому дешёвому размеру
func (p Product) Prices() (basic, sale int64) {
	for _, s := range p.Sizes {
		if s.Price == nil || s.Price.Product == 0 {
			continue
		}
		if sale == 0 || s.Price.Product < sale {
			basic, sale = s.Price.Basic, s.Price.Product
		}
	}
	return basic, sale
}
