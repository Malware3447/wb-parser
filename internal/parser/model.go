package parser

// Product представляет информацию о товаре
type Product struct {
	Name  string `json:"name"`
	Price string `json:"price"`
	URL   string `json:"url"`
	Image string `json:"image"`
}
