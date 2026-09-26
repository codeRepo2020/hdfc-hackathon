package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type mode string

const (
	modeHealthy     mode = "healthy"
	modeSlow        mode = "slow"
	modeDown        mode = "down"
	modeConflicting mode = "conflicting"
)

type item struct {
	ID        string `json:"itemId"`
	Name      string `json:"name"`
	Available int    `json:"available"`
}

type reservation struct {
	ID     string `json:"reservationId"`
	ItemID string `json:"itemId"`
	UserID string `json:"userId"`
	Qty    int    `json:"qty"`
	Status string `json:"status"`
}

type server struct {
	mu           sync.Mutex
	mode         mode
	items        map[string]*item
	reservations map[string]reservation
}

func newServer() *server {
	s := &server{
		mode:         modeHealthy,
		items:        map[string]*item{},
		reservations: map[string]reservation{},
	}
	s.seed()
	return s
}

func (s *server) seed() {
	s.items = map[string]*item{
		"hype-001": {ID: "hype-001", Name: "Hype Drop 001", Available: 50},
		"hype-002": {ID: "hype-002", Name: "Court Classic", Available: 20},
		"hype-003": {ID: "hype-003", Name: "Night Runner", Available: 10},
		"hype-004": {ID: "hype-004", Name: "Studio Pack", Available: 5},
		"hype-005": {ID: "hype-005", Name: "Daily Tee", Available: 100},
	}
	s.reservations = map[string]reservation{}
}

func (s *server) reportedAvailable(it *item) int {
	if s.mode == modeConflicting {
		return 0
	}
	return it.Available
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) intercept(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		return false
	}
	s.mu.Lock()
	m := s.mode
	s.mu.Unlock()
	switch m {
	case modeDown:
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return true
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return true
		}
		_ = conn.Close()
		return true
	case modeSlow:
		time.Sleep(time.Duration(3000+rand.Intn(5000)) * time.Millisecond)
	}
	return false
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if s.intercept(w, r) {
		return
	}
	s.mu.Lock()
	m := s.mode
	s.mu.Unlock()
	if m == modeDown {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "mode": string(m)})
}

func (s *server) getItem(w http.ResponseWriter, r *http.Request) {
	if s.intercept(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/items/")
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"itemId":    it.ID,
		"name":      it.Name,
		"available": s.reportedAvailable(it),
	})
}

func (s *server) postReservations(w http.ResponseWriter, r *http.Request) {
	if s.intercept(w, r) {
		return
	}
	var req struct {
		ReservationID string `json:"reservationId"`
		ItemID        string `json:"itemId"`
		UserID        string `json:"userId"`
		Qty           int    `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReservationID == "" || req.ItemID == "" || req.Qty <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.reservations[req.ReservationID]; ok {
		it := s.items[existing.ItemID]
		avail := 0
		if it != nil {
			avail = s.reportedAvailable(it)
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"reservationId": existing.ID,
			"status":        existing.Status,
			"available":     avail,
		})
		return
	}
	it, ok := s.items[req.ItemID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	reported := s.reportedAvailable(it)
	if reported < req.Qty {
		writeJSON(w, http.StatusConflict, map[string]any{
			"reservationId": req.ReservationID,
			"status":        "rejected",
			"reason":        "insufficient_stock",
			"available":     reported,
		})
		return
	}
	it.Available -= req.Qty
	res := reservation{ID: req.ReservationID, ItemID: req.ItemID, UserID: req.UserID, Qty: req.Qty, Status: "confirmed"}
	s.reservations[req.ReservationID] = res
	writeJSON(w, http.StatusCreated, map[string]any{
		"reservationId": res.ID,
		"status":        "confirmed",
		"available":     s.reportedAvailable(it),
	})
}

func (s *server) postReleases(w http.ResponseWriter, r *http.Request) {
	if s.intercept(w, r) {
		return
	}
	var req struct {
		ReservationID string `json:"reservationId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReservationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.reservations[req.ReservationID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if res.Status == "confirmed" {
		if it := s.items[res.ItemID]; it != nil {
			it.Available += res.Qty
		}
		res.Status = "released"
		s.reservations[req.ReservationID] = res
	}
	avail := 0
	if it := s.items[res.ItemID]; it != nil {
		avail = s.reportedAvailable(it)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reservationId": res.ID,
		"status":        "released",
		"available":     avail,
	})
}

func (s *server) adminMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	m := mode(req.Mode)
	switch m {
	case modeHealthy, modeSlow, modeDown, modeConflicting:
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown_mode"})
		return
	}
	s.mu.Lock()
	s.mode = m
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"mode": string(m)})
}

func (s *server) adminReset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.mode = modeHealthy
	s.seed()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *server) adminStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"mode": s.mode, "reservations": len(s.reservations)})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	rand.Seed(time.Now().UnixNano())
	s := newServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /items/", s.getItem)
	mux.HandleFunc("POST /reservations", s.postReservations)
	mux.HandleFunc("POST /releases", s.postReleases)
	mux.HandleFunc("POST /admin/mode", s.adminMode)
	mux.HandleFunc("POST /admin/reset", s.adminReset)
	mux.HandleFunc("GET /admin/status", s.adminStatus)

	addr := ":9000"
	if v := os.Getenv("PORT"); v != "" {
		addr = ":" + v
	}
	log.Printf("authority listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, withCORS(mux)))
}
