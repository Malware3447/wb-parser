package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"wb-parser/internal/parser"
)

func main() {
	queriesPath := flag.String("queries", "queries.csv", "файл со списком «SKU;запрос»")
	outDir := flag.String("out", "data", "папка для отчётов")
	top := flag.Int("top", 30, "сколько первых товаров выдачи сохранять")
	fetcher := flag.String("fetcher", "browser/fetch.mjs", "скрипт, получающий выдачу через браузер")
	headed := flag.Bool("headed", false, "показывать окно браузера")
	raw := flag.Bool("raw", false, "сохранять полные ответы Wildberries")
	flag.Parse()

	tasks, err := parser.LoadTasks(*queriesPath)
	if err != nil {
		log.Fatalf("Ошибка чтения списка запросов: %v", err)
	}
	queries := parser.UniqueQueries(tasks)
	fmt.Printf("SKU и запросов: %d, разных запросов: %d\n", len(tasks), len(queries))

	results, fetchErr := parser.Fetch(*fetcher, queries, *headed)
	if len(results) == 0 {
		log.Fatalf("Ошибка получения выдачи: %v", fetchErr)
	}

	report, err := parser.WriteReport(*outDir, time.Now(), tasks, results, *top, *raw)
	if err != nil {
		log.Fatalf("Ошибка записи отчёта: %v", err)
	}

	fmt.Printf("\n--- Готово ---\n")
	fmt.Printf("Выдача (топ-%d): %s\n", *top, report.TopFile)
	fmt.Printf("Сводка по SKU:   %s\n", report.SummaryFile)
	fmt.Printf("Запросов получено: %d из %d\n", report.OK, len(queries))
	if fetchErr != nil {
		log.Fatalf("Часть запросов не получена: %v", fetchErr)
	}
}
