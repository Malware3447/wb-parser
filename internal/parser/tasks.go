package parser

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LoadTasks читает файл со строками «SKU;запрос». Пустые строки и строки с # пропускаются
func LoadTasks(path string) ([]Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tasks []Task
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		skuPart, query, ok := strings.Cut(line, ";")
		query = strings.TrimSpace(query)
		if !ok || query == "" {
			return nil, fmt.Errorf("строка %d: ожидается «SKU;запрос»", n)
		}
		sku, err := strconv.ParseInt(strings.TrimSpace(skuPart), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("строка %d: SKU должен быть числом", n)
		}
		tasks = append(tasks, Task{SKU: sku, Query: query})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("в файле %s нет ни одной строки «SKU;запрос»", path)
	}
	return tasks, nil
}

// UniqueQueries возвращает запросы без повторов в порядке появления
func UniqueQueries(tasks []Task) []string {
	seen := make(map[string]bool)
	var queries []string
	for _, t := range tasks {
		if !seen[t.Query] {
			seen[t.Query] = true
			queries = append(queries, t.Query)
		}
	}
	return queries
}
