package parser

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// ParseAndSaveHTML парсит страницу с сайта Wildberries по заданному поисковому запросу и сохраняет HTML в файл
func ParseAndSaveHTML(searchQuery string) error {
	// Настройка параметров запуска
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		// Отключаем режим "без головы" для лучшей совместимости с WB
		chromedp.Flag("headless", false),

		// Убираем маркер "AutomationControlled"
		chromedp.Flag("disable-blink-features", "AutomationControlled"),

		// Устанавливаем обычный размер окна
		chromedp.WindowSize(1920, 1080),

		// Подменяем User-Agent на человеческий
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	// Создаем контекст
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Устанавливаем общий таймаут на всю операцию
	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Формируем URL для поиска
	searchQueryFormatted := strings.ReplaceAll(searchQuery, " ", "%20")
	baseURL := "https://www.wildberries.ru/catalog/0/search.aspx"
	searchURL := fmt.Sprintf("%s?search=%s", baseURL, searchQueryFormatted)

	var htmlContent string

	fmt.Println("Запускаю браузер. Пожалуйста, не закрывайте открывшееся окно...")

	// Выполняем цепочку действий
	err := chromedp.Run(ctx,
		chromedp.Navigate(searchURL),

		// Ждем небольшую паузу, чтобы имитировать чтение страницы человеком
		chromedp.Sleep(5*time.Second),

		// Скроллим вниз, чтобы подгрузились товары
		chromedp.Evaluate(`window.scrollTo(0, 500);`, nil),
		chromedp.Sleep(2*time.Second),

		// Забираем итоговый HTML всей страницы
		chromedp.OuterHTML("html", &htmlContent),
	)

	if err != nil {
		return fmt.Errorf("ошибка выполнения: %w", err)
	}

	// Сохраняем результат
	filename := fmt.Sprintf("wildberries_%s.html", strings.ReplaceAll(searchQuery, " ", "_"))
	err = os.WriteFile(filename, []byte(htmlContent), 0644)
	if err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}

	fmt.Printf("\n--- Готово! ---\n")
	fmt.Printf("Файл сохранен: %s\n", filename)
	fmt.Printf("Размер HTML: %d байт\n", len(htmlContent))

	return nil
}
