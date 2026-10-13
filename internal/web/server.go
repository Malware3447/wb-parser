// Package web — интерфейс: список SKU с запросами, выдача по запросу, запуск проверки
package web

import (
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"wb-parser/internal/parser"
	"wb-parser/internal/store"
)

//go:embed templates.html
var templates embed.FS

const (
	keepItems   = 100 // сколько товаров выдачи хранить
	competitors = 10  // сколько первых органических конкурентов показывать и брать в расчёт цен
	exportTop   = 30  // сколько товаров выгружать в CSV по умолчанию
	historyDays = 7   // сколько дней показывать в истории позиций
)

// Region — регион Wildberries и его код в запросах
type Region struct {
	Name string
	Dest int64
}

// Regions — коды, которые Wildberries отдаёт для центра города (кроме Москвы: её код задан заказчиком)
var Regions = []Region{
	{"Москва", -1257786},
	{"Санкт-Петербург", -1198055},
	{"Новосибирск", -364763},
	{"Екатеринбург", -5818883},
	{"Казань", -2133462},
	{"Нижний Новгород", 12358536},
	{"Краснодар", 12358062},
	{"Ростов-на-Дону", -2228342},
	{"Самара", -283781},
	{"Уфа", 12358495},
	{"Красноярск", 12356481},
	{"Хабаровск", -1785058},
	{"Владивосток", 1259571108},
}

func regionName(dest int64) string {
	for _, r := range Regions {
		if r.Dest == dest {
			return r.Name
		}
	}
	return "код " + strconv.FormatInt(dest, 10)
}

// Server отдаёт интерфейс и запускает проверки
type Server struct {
	store   *store.Store
	fetcher string
	at      string
	loc     *time.Location
	auth    string
	base    string
	tmpl    *template.Template
	mux     *http.ServeMux

	mu       sync.Mutex
	running  bool
	lastRun  time.Time
	lastErr  string
	lastDone int
}

// New создаёт сервер интерфейса.
// base — путь, под которым интерфейс виден снаружи (например, /wb), пустой для корня
func New(st *store.Store, fetcher, at string, loc *time.Location, auth, base string) *Server {
	s := &Server{store: st, fetcher: fetcher, at: at, loc: loc, auth: auth, base: strings.TrimRight(base, "/"), mux: http.NewServeMux()}
	s.tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"rub":     rubles,
		"percent": discount,
		"day":     shortDate,
		"num":     decimal,
		"idx":     formatIndex,
		"signed":  formatSigned,
		"versus":  versus,
		"statusText": func(index float64) string {
			text, _ := priceStatus(s.store.Settings(), index)
			return text
		},
		"statusClass": func(index float64) string {
			_, class := priceStatus(s.store.Settings(), index)
			return class
		},
	}).ParseFS(templates, "templates.html"))

	s.mux.HandleFunc("/", s.index)
	s.mux.HandleFunc("/sku", s.sku)
	s.mux.HandleFunc("/query", s.query)
	s.mux.HandleFunc("/export.csv", s.export)
	s.mux.HandleFunc("/sku/save", s.saveSKU)
	s.mux.HandleFunc("/sku/delete", s.deleteSKU)
	s.mux.HandleFunc("/settings", s.saveSettings)
	s.mux.HandleFunc("/run", s.run)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.auth != "" {
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user+":"+pass), []byte(s.auth)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="wb-parser"`)
			http.Error(w, "Нужен вход", http.StatusUnauthorized)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// Schedule раз в день в заданное время запускает проверку запросов, по которым сегодня ещё нет выдачи.
// Если сервер был выключен в это время, проверка пройдёт после запуска
func (s *Server) Schedule() {
	for ; ; time.Sleep(30 * time.Second) {
		now := time.Now().In(s.loc)
		today := now.Format("2006-01-02")
		if now.Format("15:04") < s.at || s.store.LastAuto() == today || len(s.store.Queries()) == 0 {
			continue
		}
		if s.Start(true) {
			if err := s.store.SetLastAuto(today); err != nil {
				log.Printf("Ошибка сохранения даты проверки: %v", err)
			}
		}
	}
}

// Start запускает проверку в фоне. onlyMissing — только запросы, по которым сегодня ещё нет выдачи
// для выбранного региона. Возвращает false, если проверка уже идёт
func (s *Server) Start(onlyMissing bool) bool {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		done, err := s.check(onlyMissing)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.running = false
		if done < 0 {
			return
		}
		s.lastRun = time.Now().In(s.loc)
		s.lastDone = done
		s.lastErr = ""
		if err != nil {
			s.lastErr = err.Error()
			log.Printf("Ошибка проверки: %v", err)
		}
	}()
	return true
}

// check получает выдачу и карточки наших товаров и сохраняет их в снимок за сегодня;
// возвращает число полученных запросов, −1 — если проверять было нечего
func (s *Server) check(onlyMissing bool) (int, error) {
	date := time.Now().In(s.loc).Format("2006-01-02")
	dest := s.store.Settings().Dest
	queries := s.store.Queries()
	var cards []int64
	for _, sku := range s.store.SKUs() {
		cards = append(cards, sku.ID)
	}
	if onlyMissing {
		snap := s.store.Snapshot(date)
		var missingQueries []string
		for _, q := range queries {
			if r, ok := snap.Queries[q]; !ok || r.Error != "" || r.Dest != dest {
				missingQueries = append(missingQueries, q)
			}
		}
		var missingCards []int64
		for _, id := range cards {
			if c, ok := snap.Cards[id]; !ok || c.Error != "" || c.Dest != dest {
				missingCards = append(missingCards, id)
			}
		}
		queries, cards = missingQueries, missingCards
	}
	if len(queries) == 0 && len(cards) == 0 {
		return -1, nil
	}

	results, fetchErr := parser.Fetch(s.fetcher, queries, cards, dest, false)
	if len(results) == 0 {
		return 0, fetchErr
	}

	done := 0
	now := time.Now().In(s.loc)
	savedQueries := make(map[string]*store.QueryResult)
	savedCards := make(map[int64]*store.Card)
	for _, r := range results {
		var resp parser.SearchResponse
		errText := r.Error
		if r.OK {
			if err := json.Unmarshal(r.Data, &resp); err != nil {
				errText = "ошибка парсинга JSON: " + err.Error()
			}
		}

		if r.Kind == "card" {
			card := &store.Card{CheckedAt: now, Dest: dest, Error: errText}
			if errText == "" {
				card.Error = "товар не найден"
				for _, p := range resp.Products {
					if p.ID == r.ID {
						basic, sale := p.Prices()
						*card = store.Card{CheckedAt: now, Dest: dest, Root: p.Root, Brand: p.Brand, Name: p.Name, Supplier: p.Supplier,
							Basic: basic, Price: sale, Rating: p.ReviewRating, Feedbacks: p.Feedbacks}
						break
					}
				}
			}
			savedCards[r.ID] = card
			continue
		}

		qr := &store.QueryResult{CheckedAt: now, Dest: dest, Error: errText}
		if errText == "" {
			qr.Total = resp.Total
			for i, p := range resp.Products {
				if i == keepItems {
					break
				}
				basic, sale := p.Prices()
				qr.Items = append(qr.Items, store.Item{Pos: i + 1, ID: p.ID, Root: p.Root, Brand: p.Brand, Name: p.Name, Supplier: p.Supplier,
					Basic: basic, Price: sale, Rating: p.ReviewRating, Feedbacks: p.Feedbacks, Quantity: p.TotalQuantity, Ad: p.Logs != ""})
			}
			done++
		}
		savedQueries[r.Query] = qr
	}
	if err := s.store.SaveResults(date, savedQueries, savedCards); err != nil {
		return done, err
	}
	return done, fetchErr
}

// --- сводка ---

type historyCell struct {
	Date    string
	Pos     int
	Checked bool
}

// compRow — строка таблицы конкурентов
type compRow struct {
	store.Item
	Ours bool
	// Organic — место среди обычных карточек, 0 у рекламы
	Organic int
}

// analysis — разбор выдачи по одному запросу за один день
type analysis struct {
	Checked bool
	Error   string
	// Pos — место нашего товара в выдаче как есть, Organic — среди обычных карточек (0 — мы в рекламе)
	Pos, Organic int
	OurAd        bool
	OurPrice     int64
	// Цены считаются по первым обычным карточкам конкурентов: реклама и наши товары не в счёт
	Median   int64
	Min3     int64
	Index    float64
	Cheaper  int
	Compared int
	Cheapest *store.Item
	Top      []compRow
}

// queryStat — сводка по одному запросу нашего товара
type queryStat struct {
	analysis
	Query      string
	Weight     int
	Delta      int
	IndexDelta float64
	History    []historyCell
}

// skuStat — сводка по нашему товару
type skuStat struct {
	store.SKU
	Title     string
	Card      *store.Card
	Price     int64
	Queries   []queryStat
	Index     float64
	Weighted  bool
	BestPos   int
	Cheaper   int
	Compared  int
	CheckedAt time.Time
	Dest      int64
}

// isOurs сообщает, наш ли это товар: тот же артикул или другой артикул той же карточки
func isOurs(it store.Item, sku store.SKU, card *store.Card) bool {
	return it.ID == sku.ID || (card != nil && card.Root != 0 && it.Root == card.Root)
}

func goodCard(snap *store.Snapshot, id int64) *store.Card {
	if card := snap.Cards[id]; card != nil && card.Error == "" {
		return card
	}
	return nil
}

// analyze разбирает выдачу по запросу из снимка: где мы, какие цены у первых конкурентов
func analyze(sku store.SKU, snap *store.Snapshot, query string) analysis {
	var a analysis
	res, ok := snap.Queries[query]
	if !ok {
		return a
	}
	if res.Error != "" {
		a.Error = res.Error
		return a
	}
	a.Checked = true
	card := goodCard(snap, sku.ID)
	if card != nil {
		a.OurPrice = card.Price
	}

	var prices []int64
	var ourRow *compRow
	organic, shown := 0, true
	for _, it := range res.Items {
		row := compRow{Item: it, Ours: isOurs(it, sku, card)}
		if !it.Ad {
			organic++
			row.Organic = organic
		}
		if row.Ours && a.Pos == 0 {
			a.Pos, a.Organic, a.OurAd = it.Pos, row.Organic, it.Ad
			// Карточку могли не получить — тогда цена берётся из выдачи
			if a.OurPrice == 0 {
				a.OurPrice = it.Price
			}
			if !shown {
				found := row
				ourRow = &found
			}
		}
		if !shown {
			continue
		}
		a.Top = append(a.Top, row)
		if row.Ours || it.Ad || it.Price == 0 {
			continue
		}
		prices = append(prices, it.Price)
		if len(prices) <= 3 && (a.Min3 == 0 || it.Price < a.Min3) {
			a.Min3 = it.Price
		}
		if a.Cheapest == nil || it.Price < a.Cheapest.Price {
			cheapest := it
			a.Cheapest = &cheapest
		}
		shown = len(prices) < competitors
	}
	// Наш товар виден в таблице, даже когда он ниже первых конкурентов
	if ourRow != nil {
		a.Top = append(a.Top, *ourRow)
	}

	a.Median = median(prices)
	if a.OurPrice > 0 && a.Median > 0 {
		a.Index = float64(a.OurPrice) / float64(a.Median)
		a.Compared = len(prices)
		for _, p := range prices {
			if p < a.OurPrice {
				a.Cheaper++
			}
		}
	}
	return a
}

// stat собирает сводку по товару из последних снимков; dates идут от новых к старым
func (s *Server) stat(sku store.SKU, dates []string) skuStat {
	st := skuStat{SKU: sku, Title: sku.Name}
	snaps := make([]*store.Snapshot, len(dates))
	for i, d := range dates {
		snaps[i] = s.store.Snapshot(d)
	}
	if len(snaps) > 0 {
		if card := goodCard(snaps[0], sku.ID); card != nil {
			st.Card, st.Price, st.CheckedAt, st.Dest = card, card.Price, card.CheckedAt, card.Dest
			if st.Title == "" {
				st.Title = strings.TrimSpace(card.Brand + " " + card.Name)
			}
		}
	}

	var indexSum, weightSum float64
	for _, q := range sku.Queries {
		row := queryStat{Query: q, Weight: sku.Weights[q]}
		if row.Weight > 0 {
			st.Weighted = true
		}
		previous := false
		for i, snap := range snaps {
			a := analyze(sku, snap, q)
			row.History = append(row.History, historyCell{Date: dates[i], Pos: a.Pos, Checked: a.Checked})
			if i == 0 {
				row.analysis = a
				continue
			}
			// Изменения считаем относительно предыдущего дня, когда запрос проверялся
			if a.Checked && !previous && row.Checked {
				previous = true
				if a.Pos > 0 && row.Pos > 0 {
					row.Delta = a.Pos - row.Pos
				}
				if a.Index > 0 && row.Index > 0 {
					row.IndexDelta = row.Index - a.Index
				}
			}
		}
		if row.Checked {
			if res := snaps[0].Queries[q]; res.CheckedAt.After(st.CheckedAt) {
				st.CheckedAt, st.Dest = res.CheckedAt, res.Dest
			}
			if st.Price == 0 {
				st.Price = row.OurPrice
			}
			if row.Pos > 0 && (st.BestPos == 0 || row.Pos < st.BestPos) {
				st.BestPos = row.Pos
			}
		}
		st.Queries = append(st.Queries, row)
	}

	// Самые показываемые запросы — первыми; общий индекс взвешен по показам
	sort.SliceStable(st.Queries, func(i, j int) bool { return st.Queries[i].Weight > st.Queries[j].Weight })
	for _, row := range st.Queries {
		if row.Index == 0 {
			continue
		}
		// Запрос без показов весит как один показ
		weight := 1.0
		if st.Weighted && row.Weight > 0 {
			weight = float64(row.Weight)
		}
		indexSum += row.Index * weight
		weightSum += weight
		st.Cheaper += row.Cheaper
		st.Compared += row.Compared
	}
	if weightSum > 0 {
		st.Index = indexSum / weightSum
	}
	return st
}

// --- страницы ---

type status struct {
	Running  bool
	LastRun  time.Time
	LastErr  string
	LastDone int
	At       string
	Region   string
}

func (s *Server) status() status {
	region := regionName(s.store.Settings().Dest)
	s.mu.Lock()
	defer s.mu.Unlock()
	return status{Running: s.running, LastRun: s.lastRun, LastErr: s.lastErr, LastDone: s.lastDone, At: s.at, Region: region}
}

func (s *Server) recentDates() []string {
	dates := s.store.Dates()
	if len(dates) > historyDays {
		dates = dates[:historyDays]
	}
	return dates
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	dates := s.recentDates()
	var stats []skuStat
	for _, sku := range s.store.SKUs() {
		stats = append(stats, s.stat(sku, dates))
	}
	settings := s.store.Settings()
	custom := true
	for _, region := range Regions {
		if region.Dest == settings.Dest {
			custom = false
		}
	}
	s.render(w, "index", map[string]interface{}{
		"Base":        s.base,
		"Status":      s.status(),
		"SKUs":        stats,
		"Dates":       dates,
		"Settings":    settings,
		"Regions":     Regions,
		"CustomDest":  custom,
		"Competitors": competitors,
		"Notice":      r.URL.Query().Get("notice"),
	})
}

func (s *Server) sku(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	for _, sku := range s.store.SKUs() {
		if sku.ID != id {
			continue
		}
		dates := s.recentDates()
		st := s.stat(sku, dates)
		// В поле правки показы идут после запроса через «;»
		var lines []string
		for _, q := range sku.Queries {
			if w := sku.Weights[q]; w > 0 {
				q += ";" + strconv.Itoa(w)
			}
			lines = append(lines, q)
		}
		s.render(w, "sku", map[string]interface{}{
			"Base":        s.base,
			"Status":      s.status(),
			"SKU":         st,
			"Region":      regionName(st.Dest),
			"Lines":       strings.Join(lines, "\n"),
			"Dates":       dates,
			"Competitors": competitors,
			"Notice":      r.URL.Query().Get("notice"),
		})
		return
	}
	http.NotFound(w, r)
}

type itemRow struct {
	store.Item
	Ours    bool
	Organic int
	Delta   int
	New     bool
}

// oursIndex собирает артикулы и номера общих карточек всех наших товаров за дату
func (s *Server) oursIndex(date string) (ids, roots map[int64]bool) {
	ids, roots = make(map[int64]bool), make(map[int64]bool)
	snap := s.store.Snapshot(date)
	for _, sku := range s.store.SKUs() {
		ids[sku.ID] = true
		if card := goodCard(snap, sku.ID); card != nil && card.Root != 0 {
			roots[card.Root] = true
		}
	}
	return ids, roots
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	dates := s.store.Dates()
	date := r.URL.Query().Get("date")
	if date == "" && len(dates) > 0 {
		date = dates[0]
	}
	top, _ := strconv.Atoi(r.URL.Query().Get("top"))
	if top != 50 && top != 100 {
		top = 30
	}
	ids, roots := s.oursIndex(date)

	// Позиции за предыдущий день проверки — чтобы показать, кто поднялся и кто опустился
	var prev map[int64]int
	for _, d := range dates {
		if d >= date {
			continue
		}
		if res, ok := s.store.Snapshot(d).Queries[q]; ok && res.Error == "" {
			prev = make(map[int64]int, len(res.Items))
			for _, it := range res.Items {
				prev[it.ID] = it.Pos
			}
			break
		}
	}

	res := s.store.Snapshot(date).Queries[q]
	var rows []itemRow
	ads := 0
	region := ""
	if res != nil {
		region = regionName(res.Dest)
		organic := 0
		for _, it := range res.Items {
			if it.Pos > top {
				break
			}
			row := itemRow{Item: it, Ours: ids[it.ID] || roots[it.Root]}
			if it.Ad {
				ads++
			} else {
				organic++
				row.Organic = organic
			}
			if prev != nil {
				if was, ok := prev[it.ID]; ok {
					row.Delta = was - it.Pos
				} else {
					row.New = true
				}
			}
			rows = append(rows, row)
		}
	}

	s.render(w, "query", map[string]interface{}{
		"Base":   s.base,
		"Status": s.status(),
		"Query":  q,
		"Back":   r.URL.Query().Get("sku"),
		"Date":   date,
		"Dates":  dates,
		"Top":    top,
		"Tops":   []int{30, 50, 100},
		"Result": res,
		"Region": region,
		"Rows":   rows,
		"Ads":    ads,
	})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if dates := s.store.Dates(); date == "" && len(dates) > 0 {
		date = dates[0]
	}
	top, _ := strconv.Atoi(r.URL.Query().Get("top"))
	if top <= 0 {
		top = exportTop
	}
	ids, roots := s.oursIndex(date)
	snap := s.store.Snapshot(date)
	queries := make([]string, 0, len(snap.Queries))
	for q := range snap.Queries {
		queries = append(queries, q)
	}
	sort.Strings(queries)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="wb_%s_top%d.csv"`, date, top))
	// UTF-8 с BOM и разделитель «;» — так Excel открывает файл без настройки
	fmt.Fprint(w, "\ufeff")
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.UseCRLF = true
	cw.Write([]string{"Дата", "Регион", "Запрос", "Позиция", "Позиция без рекламы", "Реклама", "Артикул", "Бренд", "Название", "Продавец",
		"Цена до скидки", "Цена", "Скидка, %", "Рейтинг", "Отзывы", "Остаток", "Наш", "Ссылка"})
	for _, q := range queries {
		res := snap.Queries[q]
		organic := 0
		for _, it := range res.Items {
			if it.Pos > top {
				break
			}
			organicPos, ad := "", "да"
			if !it.Ad {
				organic++
				organicPos, ad = strconv.Itoa(organic), ""
			}
			our := ""
			if ids[it.ID] || roots[it.Root] {
				our = "да"
			}
			cw.Write([]string{date, regionName(res.Dest), q, strconv.Itoa(it.Pos), organicPos, ad, strconv.FormatInt(it.ID, 10),
				it.Brand, it.Name, it.Supplier, plainRubles(it.Basic), plainRubles(it.Price), discount(it.Basic, it.Price),
				decimal(it.Rating), strconv.Itoa(it.Feedbacks), strconv.Itoa(it.Quantity), our,
				fmt.Sprintf("https://www.wildberries.ru/catalog/%d/detail.aspx", it.ID)})
		}
	}
	cw.Flush()
}

var skuPattern = regexp.MustCompile(`\d{4,}`)

// parseQueries разбирает поле с запросами: по одному на строку, после «;» или табуляции — показы
func parseQueries(text string) ([]string, map[string]int) {
	var queries []string
	weights := make(map[string]int)
	for _, line := range strings.Split(text, "\n") {
		query := line
		if i := strings.LastIndexAny(line, ";\t"); i >= 0 {
			if w, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(line[i+1:]), " ", "")); err == nil {
				query = line[:i]
				weights[store.CleanQuery(query)] = w
			}
		}
		queries = append(queries, query)
	}
	return queries, weights
}

func (s *Server) saveSKU(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Только POST", http.StatusMethodNotAllowed)
		return
	}
	// В поле можно вставить и артикул, и ссылку на карточку товара
	id, err := strconv.ParseInt(skuPattern.FindString(r.FormValue("sku")), 10, 64)
	if err != nil {
		s.back(w, r, "/", "Не удалось разобрать артикул: нужен номер или ссылка на товар")
		return
	}
	queries, weights := parseQueries(r.FormValue("queries"))
	if err := s.store.SaveSKU(id, strings.TrimSpace(r.FormValue("name")), queries, weights); err != nil {
		s.back(w, r, "/", "Ошибка сохранения: "+err.Error())
		return
	}
	// Сразу получаем выдачу по новым запросам
	s.Start(true)
	s.back(w, r, "/sku?id="+strconv.FormatInt(id, 10), "")
}

func (s *Server) deleteSKU(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Только POST", http.StatusMethodNotAllowed)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("sku"), 10, 64)
	if err := s.store.DeleteSKU(id); err != nil {
		s.back(w, r, "/", "Ошибка удаления: "+err.Error())
		return
	}
	s.back(w, r, "/", "")
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Только POST", http.StatusMethodNotAllowed)
		return
	}
	old := s.store.Settings()
	settings := old
	// Свой код региона важнее выбранного из списка
	dest := strings.TrimSpace(r.FormValue("dest_custom"))
	if dest == "" {
		dest = r.FormValue("dest")
	}
	var err error
	if settings.Dest, err = strconv.ParseInt(dest, 10, 64); err != nil || settings.Dest == 0 {
		s.back(w, r, "/", "Код региона должен быть числом")
		return
	}
	bounds := []*float64{&settings.Underprice, &settings.GreenLo, &settings.GreenHi, &settings.YellowHi}
	for i, name := range []string{"underprice", "green_lo", "green_hi", "yellow_hi"} {
		value, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(r.FormValue(name)), ",", ".", 1), 64)
		if err != nil || value <= 0 {
			s.back(w, r, "/", "Пороги должны быть положительными числами")
			return
		}
		*bounds[i] = value
	}
	if !(settings.Underprice < settings.GreenLo && settings.GreenLo < settings.GreenHi && settings.GreenHi < settings.YellowHi) {
		s.back(w, r, "/", "Пороги должны идти по возрастанию")
		return
	}
	if err := s.store.SaveSettings(settings); err != nil {
		s.back(w, r, "/", "Ошибка сохранения: "+err.Error())
		return
	}
	// Для нового региона выдачу и цены нужно получить заново
	if settings.Dest != old.Dest {
		s.Start(true)
	}
	s.back(w, r, "/", "")
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Только POST", http.StatusMethodNotAllowed)
		return
	}
	if len(s.store.SKUs()) == 0 {
		s.back(w, r, "/", "Сначала добавьте товар с запросами")
		return
	}
	s.Start(false)
	s.back(w, r, "/", "")
}

// back возвращает на страницу интерфейса, при необходимости с сообщением
func (s *Server) back(w http.ResponseWriter, r *http.Request, path, notice string) {
	target := s.base + path
	if notice != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		target += sep + "notice=" + template.URLQueryEscaper(notice)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("Ошибка шаблона %s: %v", name, err)
	}
}

// --- форматирование ---

// median возвращает медиану цен, 0 — если цен нет
func median(prices []int64) int64 {
	if len(prices) == 0 {
		return 0
	}
	sorted := append([]int64(nil), prices...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

// priceStatus переводит индекс цены в статус и его цвет
func priceStatus(t store.Settings, index float64) (text, class string) {
	switch {
	case index == 0:
		return "", ""
	case index < t.Underprice:
		return "переуценка", "under"
	case index < t.GreenLo:
		return "чуть ниже рынка", "below"
	case index <= t.GreenHi:
		return "в рынке", "ok"
	case index <= t.YellowHi:
		return "дороже рынка", "warn"
	}
	return "сильно дороже рынка", "bad"
}

// formatIndex показывает индекс цены с двумя знаками
func formatIndex(index float64) string {
	if index == 0 {
		return "—"
	}
	return strings.Replace(strconv.FormatFloat(index, 'f', 2, 64), ".", ",", 1)
}

// formatSigned показывает изменение индекса со знаком; мелкие колебания не показываются
func formatSigned(delta float64) string {
	if delta > -0.005 && delta < 0.005 {
		return ""
	}
	text := strings.Replace(strconv.FormatFloat(delta, 'f', 2, 64), ".", ",", 1)
	if delta > 0 {
		return "+" + text
	}
	return "−" + strings.TrimPrefix(text, "-")
}

// versus показывает, на сколько процентов цена конкурента отличается от нашей
func versus(price, ours int64) string {
	if price == 0 || ours == 0 {
		return ""
	}
	diff := (price - ours) * 100 / ours
	switch {
	case diff > 0:
		return "+" + strconv.FormatInt(diff, 10) + "%"
	case diff < 0:
		return "−" + strconv.FormatInt(-diff, 10) + "%"
	}
	return "0%"
}

// rubles переводит копейки в рубли с разбивкой по разрядам; ноль означает, что цены нет
func rubles(kopecks int64) string {
	if kopecks == 0 {
		return "—"
	}
	digits := strconv.FormatInt((kopecks+50)/100, 10)
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString("\u00a0")
		}
		b.WriteRune(c)
	}
	return b.String() + "\u00a0₽"
}

func plainRubles(kopecks int64) string {
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

// decimal записывает дробное число с запятой
func decimal(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

// shortDate превращает 2026-10-13 в 13.10
func shortDate(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Format("02.01")
}
