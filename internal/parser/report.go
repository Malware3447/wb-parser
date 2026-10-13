package parser

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Report описывает, куда сохранены отчёты
type Report struct {
	TopFile     string
	SummaryFile string
	OK          int
}

// WriteReport сохраняет выдачу по запросам и сводку по отслеживаемым SKU за день
func WriteReport(outDir string, now time.Time, tasks []Task, results []Result, top int, raw bool) (Report, error) {
	date := now.Format("2006-01-02")
	report := Report{
		TopFile:     filepath.Join(outDir, date+"_top.csv"),
		SummaryFile: filepath.Join(outDir, date+"_summary.csv"),
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return report, fmt.Errorf("ошибка создания папки: %w", err)
	}

	ours := make(map[int64]bool)
	for _, t := range tasks {
		ours[t.SKU] = true
	}

	// Разбираем ответы по запросам
	products := make(map[string][]Product)
	failed := make(map[string]string)
	for _, r := range results {
		if !r.OK {
			failed[r.Query] = r.Error
			continue
		}
		var resp searchResponse
		if err := json.Unmarshal(r.Data, &resp); err != nil {
			failed[r.Query] = "ошибка парсинга JSON: " + err.Error()
			continue
		}
		products[r.Query] = resp.Products
		report.OK++

		if raw {
			rawDir := filepath.Join(outDir, date+"_raw")
			if err := os.MkdirAll(rawDir, 0755); err != nil {
				return report, fmt.Errorf("ошибка создания папки: %w", err)
			}
			name := strings.ReplaceAll(r.Query, " ", "_") + ".json"
			if err := os.WriteFile(filepath.Join(rawDir, name), r.Data, 0644); err != nil {
				return report, fmt.Errorf("ошибка при записи файла: %w", err)
			}
		}
	}

	// Выдача: первые top товаров по каждому запросу
	topRows := [][]string{{"Дата", "Запрос", "Позиция", "Артикул", "Бренд", "Название", "Продавец",
		"Цена до скидки", "Цена", "Скидка, %", "Рейтинг", "Отзывы", "Остаток", "Наш", "Ссылка"}}
	for _, query := range UniqueQueries(tasks) {
		list := products[query]
		if len(list) > top {
			list = list[:top]
		}
		for i, p := range list {
			basic, sale := p.Prices()
			topRows = append(topRows, []string{
				date, query, strconv.Itoa(i + 1), strconv.FormatInt(p.ID, 10), p.Brand, p.Name, p.Supplier,
				rubles(basic), rubles(sale), discount(basic, sale), decimal(p.ReviewRating),
				strconv.Itoa(p.Feedbacks), strconv.Itoa(p.TotalQuantity), yes(ours[p.ID]),
				fmt.Sprintf("https://www.wildberries.ru/catalog/%d/detail.aspx", p.ID),
			})
		}
	}
	if err := writeCSV(report.TopFile, topRows); err != nil {
		return report, err
	}

	// Сводка: где стоит каждый наш SKU и какие цены вокруг
	summaryRows := [][]string{{"Дата", "SKU", "Запрос", "Позиция", fmt.Sprintf("В топ-%d", top), "Наша цена",
		"Мин. цена топа", "Медиана топа", "Макс. цена топа", "Товаров получено", "Ошибка"}}
	for _, t := range tasks {
		if msg, bad := failed[t.Query]; bad {
			summaryRows = append(summaryRows, []string{date, strconv.FormatInt(t.SKU, 10), t.Query, "", "", "", "", "", "", "", msg})
			continue
		}
		list, got := products[t.Query]
		if !got {
			summaryRows = append(summaryRows, []string{date, strconv.FormatInt(t.SKU, 10), t.Query, "", "", "", "", "", "", "", "запрос не выполнен"})
			continue
		}

		position, ourPrice := "", ""
		inTop := false
		var prices []int64
		for i, p := range list {
			_, sale := p.Prices()
			if i < top && sale > 0 {
				prices = append(prices, sale)
			}
			if p.ID == t.SKU {
				position = strconv.Itoa(i + 1)
				ourPrice = rubles(sale)
				inTop = i < top
			}
		}
		if position == "" {
			position = fmt.Sprintf("нет в первых %d", len(list))
		}
		minPrice, median, maxPrice := priceStats(prices)
		summaryRows = append(summaryRows, []string{date, strconv.FormatInt(t.SKU, 10), t.Query, position, yes(inTop), ourPrice,
			rubles(minPrice), rubles(median), rubles(maxPrice), strconv.Itoa(len(list)), ""})
	}
	if err := writeCSV(report.SummaryFile, summaryRows); err != nil {
		return report, err
	}

	return report, nil
}

// writeCSV сохраняет таблицу в виде, который Excel открывает без настройки: UTF-8 с BOM и разделитель «;»
func writeCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString("\ufeff"); err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}
	w := csv.NewWriter(f)
	w.Comma = ';'
	w.UseCRLF = true
	if err := w.WriteAll(rows); err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}
	return nil
}

func priceStats(prices []int64) (minPrice, median, maxPrice int64) {
	if len(prices) == 0 {
		return 0, 0, 0
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
	n := len(prices)
	median = prices[n/2]
	if n%2 == 0 {
		median = (prices[n/2-1] + prices[n/2]) / 2
	}
	return prices[0], median, prices[n-1]
}

// rubles переводит копейки в рубли; ноль означает, что цены нет
func rubles(kopecks int64) string {
	if kopecks == 0 {
		return ""
	}
	if kopecks%100 == 0 {
		return strconv.FormatInt(kopecks/100, 10)
	}
	return decimal(float64(kopecks) / 100)
}

func discount(basic, sale int64) string {
	if basic <= 0 || sale <= 0 || sale >= basic {
		return ""
	}
	return strconv.FormatInt((basic-sale)*100/basic, 10)
}

// decimal записывает дробное число с запятой, как ждёт русский Excel
func decimal(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

func yes(b bool) string {
	if b {
		return "да"
	}
	return ""
}
