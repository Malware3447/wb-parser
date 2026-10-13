// Package store хранит список отслеживаемых SKU и ежедневные снимки выдачи в JSON-файлах
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SKU — отслеживаемый товар и прикреплённые к нему поисковые запросы
type SKU struct {
	ID      int64    `json:"sku"`
	Name    string   `json:"name"`
	Queries []string `json:"queries"`
	// Weights — показы запроса в рекламе: по ним запросы упорядочены и взвешен общий индекс
	Weights map[string]int `json:"weights,omitempty"`
}

// Settings — настройки, которые меняются из интерфейса
type Settings struct {
	// Dest — код региона Wildberries, от него зависят цены и выдача
	Dest int64 `json:"dest"`
	// Границы индекса цены (наша цена к медиане конкурентов), по которым ставится статус
	Underprice float64 `json:"underprice"`
	GreenLo    float64 `json:"green_lo"`
	GreenHi    float64 `json:"green_hi"`
	YellowHi   float64 `json:"yellow_hi"`
}

// DefaultSettings — Москва и пороги, согласованные с заказчиком
var DefaultSettings = Settings{Dest: -1257786, Underprice: 0.90, GreenLo: 0.95, GreenHi: 1.05, YellowHi: 1.15}

// Item — товар в выдаче
type Item struct {
	Pos       int     `json:"pos"`
	ID        int64   `json:"id"`
	Root      int64   `json:"root,omitempty"`
	Brand     string  `json:"brand"`
	Name      string  `json:"name"`
	Supplier  string  `json:"supplier"`
	Basic     int64   `json:"basic"`
	Price     int64   `json:"price"`
	Rating    float64 `json:"rating"`
	Feedbacks int     `json:"feedbacks"`
	Quantity  int     `json:"quantity"`
	// Ad — рекламное размещение
	Ad bool `json:"ad,omitempty"`
}

// QueryResult — выдача по одному запросу
type QueryResult struct {
	CheckedAt time.Time `json:"checked_at"`
	Dest      int64     `json:"dest,omitempty"`
	Total     int       `json:"total"`
	Error     string    `json:"error,omitempty"`
	Items     []Item    `json:"items"`
}

// Card — карточка нашего товара: цена и прочее, даже когда товара нет в выдаче
type Card struct {
	CheckedAt time.Time `json:"checked_at"`
	Dest      int64     `json:"dest,omitempty"`
	Error     string    `json:"error,omitempty"`
	// Root — номер общей карточки: по нему узнаются другие артикулы того же товара
	Root      int64   `json:"root,omitempty"`
	Brand     string  `json:"brand"`
	Name      string  `json:"name"`
	Supplier  string  `json:"supplier"`
	Basic     int64   `json:"basic"`
	Price     int64   `json:"price"`
	Rating    float64 `json:"rating"`
	Feedbacks int     `json:"feedbacks"`
}

// Snapshot — выдача по всем запросам и карточки наших товаров за один день
type Snapshot struct {
	Date    string                  `json:"date"`
	Queries map[string]*QueryResult `json:"queries"`
	Cards   map[int64]*Card         `json:"cards"`
}

func newSnapshot(date string) *Snapshot {
	return &Snapshot{Date: date, Queries: make(map[string]*QueryResult), Cards: make(map[int64]*Card)}
}

type state struct {
	SKUs     []SKU     `json:"skus"`
	LastAuto string    `json:"last_auto"`
	Settings *Settings `json:"settings,omitempty"`
}

// Store — хранилище в папке dir: state.json и snapshots/ДАТА.json
type Store struct {
	mu    sync.Mutex
	dir   string
	state state
	cache map[string]*Snapshot
}

// Open открывает хранилище, создавая папку при необходимости
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0755); err != nil {
		return nil, fmt.Errorf("ошибка создания папки: %w", err)
	}
	s := &Store{dir: dir, cache: make(map[string]*Snapshot)}
	body, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения списка SKU: %w", err)
	}
	if err := json.Unmarshal(body, &s.state); err != nil {
		return nil, fmt.Errorf("ошибка парсинга списка SKU: %w", err)
	}
	return s, nil
}

// SKUs возвращает копию списка отслеживаемых товаров
func (s *Store) SKUs() []SKU {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]SKU, len(s.state.SKUs))
	for i, sku := range s.state.SKUs {
		sku.Queries = append([]string(nil), sku.Queries...)
		weights := make(map[string]int, len(sku.Weights))
		for q, w := range sku.Weights {
			weights[q] = w
		}
		sku.Weights = weights
		list[i] = sku
	}
	return list
}

// Queries возвращает все запросы без повторов в порядке появления
func (s *Store) Queries() []string {
	seen := make(map[string]bool)
	var queries []string
	for _, sku := range s.SKUs() {
		for _, q := range sku.Queries {
			if !seen[q] {
				seen[q] = true
				queries = append(queries, q)
			}
		}
	}
	return queries
}

// SaveSKU добавляет товар или заменяет его название, запросы и их показы
func (s *Store) SaveSKU(id int64, name string, queries []string, weights map[string]int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	queries = cleanQueries(queries)
	kept := make(map[string]int)
	for _, q := range queries {
		if w := weights[q]; w > 0 {
			kept[q] = w
		}
	}
	for i := range s.state.SKUs {
		if s.state.SKUs[i].ID == id {
			s.state.SKUs[i].Name = name
			s.state.SKUs[i].Queries = queries
			s.state.SKUs[i].Weights = kept
			return s.saveState()
		}
	}
	s.state.SKUs = append(s.state.SKUs, SKU{ID: id, Name: name, Queries: queries, Weights: kept})
	return s.saveState()
}

// Settings возвращает настройки; пока их не меняли — значения по умолчанию
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Settings == nil {
		return DefaultSettings
	}
	return *s.state.Settings
}

// SaveSettings сохраняет настройки
func (s *Store) SaveSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Settings = &settings
	return s.saveState()
}

// DeleteSKU убирает товар из списка; снимки выдачи остаются
func (s *Store) DeleteSKU(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.state.SKUs[:0]
	for _, sku := range s.state.SKUs {
		if sku.ID != id {
			kept = append(kept, sku)
		}
	}
	s.state.SKUs = kept
	return s.saveState()
}

// LastAuto возвращает дату последней проверки по расписанию
func (s *Store) LastAuto() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LastAuto
}

// SetLastAuto запоминает дату последней проверки по расписанию
func (s *Store) SetLastAuto(date string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastAuto = date
	return s.saveState()
}

// Dates возвращает даты, за которые есть снимки, от новых к старым
func (s *Store) Dates() []string {
	entries, _ := os.ReadDir(filepath.Join(s.dir, "snapshots"))
	var dates []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".json") {
			dates = append(dates, strings.TrimSuffix(name, ".json"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

// Snapshot возвращает снимок за дату; если его нет — пустой
func (s *Store) Snapshot(date string) *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot(date)
}

func (s *Store) snapshot(date string) *Snapshot {
	if snap, ok := s.cache[date]; ok {
		return snap
	}
	snap := newSnapshot(date)
	body, err := os.ReadFile(s.snapshotPath(date))
	if err == nil {
		if err := json.Unmarshal(body, snap); err != nil {
			snap = newSnapshot(date)
		}
		if snap.Queries == nil {
			snap.Queries = make(map[string]*QueryResult)
		}
		if snap.Cards == nil {
			snap.Cards = make(map[int64]*Card)
		}
	}
	s.cache[date] = snap
	return snap
}

// SaveResults дописывает результаты в снимок за дату.
// Неудачная проверка не затирает удачную за тот же день
func (s *Store) SaveResults(date string, results map[string]*QueryResult, cards map[int64]*Card) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.snapshot(date)
	snap := newSnapshot(date)
	for q, r := range old.Queries {
		snap.Queries[q] = r
	}
	for id, c := range old.Cards {
		snap.Cards[id] = c
	}
	for q, r := range results {
		if prev, ok := snap.Queries[q]; ok && r.Error != "" && prev.Error == "" {
			continue
		}
		snap.Queries[q] = r
	}
	for id, c := range cards {
		if prev, ok := snap.Cards[id]; ok && c.Error != "" && prev.Error == "" {
			continue
		}
		snap.Cards[id] = c
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := writeFile(s.snapshotPath(date), body); err != nil {
		return err
	}
	s.cache[date] = snap
	return nil
}

func (s *Store) snapshotPath(date string) string {
	return filepath.Join(s.dir, "snapshots", filepath.Base(date)+".json")
}

func (s *Store) saveState() error {
	body, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(s.dir, "state.json"), body)
}

// writeFile пишет через временный файл, чтобы сбой не оставил половину данных
func writeFile(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0644); err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}
	return nil
}

// CleanQuery убирает лишние пробелы в запросе
func CleanQuery(q string) string {
	return strings.Join(strings.Fields(q), " ")
}

func cleanQueries(queries []string) []string {
	seen := make(map[string]bool)
	var clean []string
	for _, q := range queries {
		q = CleanQuery(q)
		if q != "" && !seen[q] {
			seen[q] = true
			clean = append(clean, q)
		}
	}
	return clean
}
