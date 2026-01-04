package main

import (
	"fmt"
	"log"
	"os"

	"wb-parser/internal/parser"
)

func main() {
	// Проверяем, передан ли поисковый запрос
	if len(os.Args) < 2 {
		log.Fatal("Пожалуйста, укажите поисковый запрос. Пример: go run main.go \"куртки мужские\"")
	}

	// Получаем поисковый запрос из аргументов командной строки
	searchQuery := os.Args[1]
	fmt.Printf("Поиск товаров по запросу: %s\n", searchQuery)

	// Парсим и сохраняем HTML страницу
	err := parser.ParseAndSaveHTML(searchQuery)
	if err != nil {
		log.Fatalf("Ошибка при парсинге: %v", err)
	}
}
