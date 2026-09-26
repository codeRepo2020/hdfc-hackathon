// Student Reservation API — thin proxy with two launch-day bugs:
//  1. Retries create a second reservation (no Idempotency-Key handling).
//  2. When the authority is slow or down, every request fails.
//
// Your job is to fix those. Do not modify the check runner or the authority.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type api struct {
	db        *pgxpool.Pool
	authority string
	client    *http.Client
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *api) health(w http.ResponseWriter, r *http.Request) {
	// Always reports live/healthy — even when the authority is gone.
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"authority": "healthy",
		"mode":      "live",
	})
}

func (a *api) getItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/items/")
	resp, err := a.client.Get(a.authority + "/items/" + id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "authority_unreachable"})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (a *api) postReservations(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemID string `json:"itemId"`
		UserID string `json:"userId"`
		Qty    int    `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ItemID == "" || req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	if req.Qty <= 0 {
		req.Qty = 1
	}
	// BUG: ignore Idempotency-Key and mint a new id every time.
	rid := newID()
	body, _ := json.Marshal(map[string]any{
		"reservationId": rid,
		"itemId":        req.ItemID,
		"userId":        req.UserID,
		"qty":           req.Qty,
	})
	resp, err := a.client.Post(a.authority+"/reservations", "application/json", strings.NewReader(string(body)))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "authority_unreachable"})
		return
	}
	defer resp.Body.Close()
	var auth map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&auth)
	status := "rejected"
	if resp.StatusCode == http.StatusCreated {
		status = "confirmed"
	}
	_, _ = a.db.Exec(r.Context(),
		`INSERT INTO reservations (id, item_id, user_id, qty, status) VALUES ($1,$2,$3,$4,$5)`,
		rid, req.ItemID, req.UserID, req.Qty, status)
	out := map[string]any{
		"reservationId": rid,
		"status":        status,
		"mode":          "live",
	}
	if reason, ok := auth["reason"]; ok {
		out["reason"] = reason
	}
	code := http.StatusCreated
	if status != "confirmed" {
		code = http.StatusConflict
	}
	writeJSON(w, code, out)
}

func (a *api) getReservation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/reservations/")
	if id == "" || strings.Contains(id, "?") {
		a.listReservations(w, r)
		return
	}
	var itemID, userID, status string
	var qty int
	err := a.db.QueryRow(r.Context(),
		`SELECT item_id, user_id, qty, status FROM reservations WHERE id=$1`, id).
		Scan(&itemID, &userID, &qty, &status)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reservationId": id,
		"itemId":        itemID,
		"userId":        userID,
		"qty":           qty,
		"status":        status,
		"mode":          "live",
	})
}

func (a *api) listReservations(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	rows, err := a.db.Query(r.Context(),
		`SELECT id, item_id, user_id, qty, status FROM reservations WHERE ($1='' OR user_id=$1) ORDER BY created_at`, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query"})
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id, itemID, uid, status string
		var qty int
		_ = rows.Scan(&id, &itemID, &uid, &qty, &status)
		list = append(list, map[string]any{
			"reservationId": id, "itemId": itemID, "userId": uid, "qty": qty, "status": status, "mode": "live",
		})
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) adminReconcile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{"replayed": 0, "confirmed": 0, "reversed": 0})
}

func (a *api) adminReset(w http.ResponseWriter, r *http.Request) {
	_, _ = a.db.Exec(r.Context(), `DELETE FROM reservations`)
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
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
	dbURL := getenv("DATABASE_URL", "postgres://launchday:launchday@localhost:5432/student?sslmode=disable")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	a := &api{
		db:        db,
		authority: strings.TrimRight(getenv("AUTHORITY_URL", "http://127.0.0.1:9000"), "/"),
		client:    &http.Client{Timeout: 2 * time.Second},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /items/", a.getItem)
	mux.HandleFunc("POST /reservations", a.postReservations)
	mux.HandleFunc("GET /reservations/{id}", a.getReservation)
	mux.HandleFunc("GET /reservations", a.listReservations)
	mux.HandleFunc("POST /admin/reconcile", a.adminReconcile)
	mux.HandleFunc("POST /admin/reset", a.adminReset)

	addr := ":" + getenv("PORT", "8080")
	log.Printf("reservation api (student) listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, withCORS(mux)))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
