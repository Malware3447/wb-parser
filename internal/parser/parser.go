package parser

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// ParseAndSaveHTML парсит страницу с сайта Wildberries по заданному поисковому запросу и сохраняет данные в файл
func ParseAndSaveHTML(searchQuery string) error {
	// Формируем URL для API запроса
	apiURL := "https://www.wildberries.ru/__internal/u-search/exactmatch/ru/common/v18/search"

	// Кодируем поисковый запрос
	query := url.QueryEscape(searchQuery)

	// Формируем полный URL с параметрами как в примере curl
	fullURL := fmt.Sprintf("%s?ab_testing=false&appType=1&curr=rub&dest=-1257786&hide_dtype=9&hide_vflags=4294967296&inheritFilters=false&lang=ru&page=1&query=%s&resultset=catalog&sort=popular&spp=30&suppressSpellcheck=false", apiURL, query)

	// Создаем HTTP клиент
	client := &http.Client{}

	// Создаем запрос
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return fmt.Errorf("ошибка создания запроса: %w", err)
	}

	// Устанавливаем заголовки точно как в примере curl
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:146.0) Gecko/20100101 Firefox/146.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Referer", fmt.Sprintf("https://www.wildberries.ru/catalog/0/search.aspx?page=1&sort=popular&search=%s", url.QueryEscape(searchQuery)))
	req.Header.Set("deviceid", "site_7ed237e8856f4243bef25baec4b6f607")
	req.Header.Set("x-queryid", "qid403661155175208351720260104211649")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	req.Header.Set("x-spa-version", "13.19.4")
	req.Header.Set("x-userid", "0")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Priority", "u=4")
	req.Header.Set("TE", "trailers")

	// Устанавливаем cookie как в примере curl
	cookie := "_wbauid=4036611551752083517; _ga_TXRZMJQDFE=GS2.1.s1758894238$o2$g0$t1758894238$j60$l0$h0; _ga=GA1.1.740072104.1758891933; x_wbaas_token=1.1000.253722bdb1af478cba7db5cad2bfde54.MTV8ODQuMzIuMTUuMTU2fE1vemlsbGEvNS4wIChXaW5kb3dzIE5UIDEwLjA7IFdpbjY0OyB4NjQ7IHJ2OjE0Ni4wKSBHZWNrby8yMDEwMDEwMSBGaXJlZm94LzE0Ni4wfDE3Njg3NTUzNjF8cmV1c2FibGV8MnxleUpvWVhOb0lqb2lJbjA9fDB8M3wxNzY4MTUwNTYxfDE=.MEUCIQCxKfGS26h1ZARZOfL8joms2RRfJ4maMEBgK/3kzvxWuQIgYo1zhx5sDk+G64HzzpOyJ/6u3ifu4+KVmV5qgIKcflk=; _cp=1; _wbauid=3824035091767561488"
	req.Header.Set("Cookie", cookie)

	// Выполняем запрос
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка выполнения запроса: %w", err)
	}
	defer resp.Body.Close()

	// Читаем и декодируем тело ответа
	var body []byte

	// Проверяем, сжат ли ответ
	if resp.Header.Get("Content-Encoding") == "gzip" {
		// Создаем декодер gzip
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("ошибка создания gzip декодера: %w", err)
		}
		defer gzipReader.Close()

		// Читаем декодированное тело ответа
		body, err = io.ReadAll(gzipReader)
		if err != nil {
			return fmt.Errorf("ошибка чтения декодированного ответа: %w", err)
		}
	} else {
		// Читаем тело ответа без декодирования
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("ошибка чтения ответа: %w", err)
		}
	}

	// Проверяем статус ответа
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("сервер вернул статус %d: %s", resp.StatusCode, string(body))
	}

	// Парсим JSON ответ
	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return fmt.Errorf("ошибка парсинга JSON: %w", err)
	}

	// Форматируем JSON для лучшей читаемости
	formattedJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка форматирования JSON: %w", err)
	}

	// Сохраняем полный ответ в файл
	filename := fmt.Sprintf("wildberries_%s.json", strings.ReplaceAll(searchQuery, " ", "_"))
	err = os.WriteFile(filename, formattedJSON, 0644)
	if err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}

	// Извлекаем ID продуктов и сохраняем их в отдельный файл
	productIDs, err := extractProductIDs(response)
	if err != nil {
		return fmt.Errorf("ошибка извлечения ID продуктов: %w", err)
	}

	// Сохраняем ID продуктов в отдельный файл
	idsFilename := fmt.Sprintf("product_ids_%s.txt", strings.ReplaceAll(searchQuery, " ", "_"))
	err = saveProductIDs(idsFilename, productIDs)
	if err != nil {
		return fmt.Errorf("ошибка сохранения ID продуктов: %w", err)
	}

	fmt.Printf("\n--- Готово! ---\n")
	fmt.Printf("Полный ответ сохранен: %s\n", filename)
	fmt.Printf("ID продуктов сохранены: %s\n", idsFilename)
	fmt.Printf("Количество продуктов: %d\n", len(productIDs))
	fmt.Printf("Статус ответа: %d\n", resp.StatusCode)

	return nil
}

// extractProductIDs извлекает ID продуктов из JSON ответа
func extractProductIDs(response map[string]interface{}) ([]int, error) {
	var productIDs []int

	// Проверяем, есть ли поле "products" в ответе
	products, ok := response["products"].([]interface{})
	if !ok {
		return productIDs, fmt.Errorf("поле 'products' не найдено в ответе или имеет неверный формат")
	}

	// Извлекаем ID для каждого продукта
	for _, product := range products {
		productMap, ok := product.(map[string]interface{})
		if !ok {
			continue
		}

		// Извлекаем ID продукта
		id, ok := productMap["id"]
		if !ok {
			continue
		}

		// Преобразуем ID в int
		idFloat, ok := id.(float64)
		if !ok {
			continue
		}

		productIDs = append(productIDs, int(idFloat))
	}

	return productIDs, nil
}

// saveProductIDs сохраняет ID продуктов в текстовый файл
func saveProductIDs(filename string, productIDs []int) error {
	// Создаем строку с ID, разделенными новой строкой
	var content strings.Builder
	for i, id := range productIDs {
		if i > 0 {
			content.WriteString("\n")
		}
		content.WriteString(strconv.Itoa(id))
	}

	// Сохраняем в файл
	err := os.WriteFile(filename, []byte(content.String()), 0644)
	if err != nil {
		return fmt.Errorf("ошибка записи файла с ID: %w", err)
	}

	return nil
}
