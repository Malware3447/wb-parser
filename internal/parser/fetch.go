package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Fetch получает через браузерный скрипт выдачу по запросам и карточки товаров для региона dest.
// Возвращает всё, что удалось получить, даже если часть запросов завершилась ошибкой
func Fetch(fetcher string, queries []string, cards []int64, dest int64, headed bool) ([]Result, error) {
	tmp, err := os.MkdirTemp("", "wb-parser-")
	if err != nil {
		return nil, fmt.Errorf("ошибка создания временной папки: %w", err)
	}
	defer os.RemoveAll(tmp)

	input, err := json.Marshal(map[string]interface{}{"queries": queries, "cards": cards, "dest": dest, "headed": headed})
	if err != nil {
		return nil, err
	}
	inputPath := filepath.Join(tmp, "input.json")
	outputPath := filepath.Join(tmp, "output.ndjson")
	if err := os.WriteFile(inputPath, input, 0644); err != nil {
		return nil, fmt.Errorf("ошибка записи списка запросов: %w", err)
	}

	cmd := exec.Command("node", fetcher, inputPath, outputPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	results, err := readResults(outputPath)
	if err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("ошибка запуска браузерного скрипта: %w", runErr)
		}
		return nil, err
	}

	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	if failed > 0 {
		return results, fmt.Errorf("не получено запросов и карточек: %d", failed)
	}
	if runErr != nil {
		return results, fmt.Errorf("браузерный скрипт завершился с ошибкой: %w", runErr)
	}
	return results, nil
}

func readResults(path string) ([]Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения результатов: %w", err)
	}
	defer f.Close()

	var results []Result
	decoder := json.NewDecoder(bufio.NewReader(f))
	for decoder.More() {
		var r Result
		if err := decoder.Decode(&r); err != nil {
			return results, fmt.Errorf("ошибка парсинга результатов: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}
