package parser

// Product представляет информацию о товаре
type Product struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price string `json:"price"`
	URL   string `json:"url"`
	Image string `json:"image"`
}

// WBResponse представляет структуру ответа от Wildberries API
type WBResponse struct {
	Products []Product `json:"products"`
}
