package parser

import "encoding/json"

// Result — ответ Wildberries: выдача по запросу (kind "query") или карточка товара (kind "card")
type Result struct {
	Kind  string          `json:"kind"`
	Query string          `json:"query"`
	ID    int64           `json:"id"`
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	URL   string          `json:"url"`
	Data  json.RawMessage `json:"data"`
}

// SearchResponse — нужные поля ответа Wildberries; у выдачи и у карточки товара они одинаковые
type SearchResponse struct {
	Total    int       `json:"total"`
	Products []Product `json:"products"`
}

// Product — товар из выдачи
type Product struct {
	ID            int64   `json:"id"`
	Root          int64   `json:"root"`
	Name          string  `json:"name"`
	Brand         string  `json:"brand"`
	Supplier      string  `json:"supplier"`
	ReviewRating  float64 `json:"reviewRating"`
	Feedbacks     int     `json:"feedbacks"`
	TotalQuantity int     `json:"totalQuantity"`
	// Logs — зашифрованная метка рекламного размещения; у обычных карточек её нет
	Logs  string `json:"logs"`
	Sizes []struct {
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
