package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"
	_ "time/tzdata"

	"wb-parser/internal/store"
	"wb-parser/internal/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "адрес, на котором открывается интерфейс")
	dataDir := flag.String("data", "data", "папка с данными")
	at := flag.String("at", "09:00", "время ежедневной проверки, ЧЧ:ММ")
	tz := flag.String("tz", "Europe/Moscow", "часовой пояс расписания")
	fetcher := flag.String("fetcher", "browser/fetch.mjs", "скрипт, получающий выдачу через браузер")
	auth := flag.String("auth", os.Getenv("WB_AUTH"), "логин:пароль для входа в интерфейс (или переменная WB_AUTH)")
	base := flag.String("base", "", "путь, под которым интерфейс виден снаружи, например /wb")
	flag.Parse()

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatalf("Ошибка часового пояса: %v", err)
	}
	if _, err := time.Parse("15:04", *at); err != nil {
		log.Fatalf("Время проверки должно быть в виде ЧЧ:ММ: %v", err)
	}
	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("Ошибка открытия данных: %v", err)
	}

	srv := web.New(st, *fetcher, *at, loc, *auth, *base)
	go srv.Schedule()

	log.Printf("Интерфейс: http://%s, ежедневная проверка в %s (%s)", *addr, *at, *tz)
	log.Fatal(http.ListenAndServe(*addr, srv))
}
