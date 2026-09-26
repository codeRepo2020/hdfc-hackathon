package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type client struct {
	base string
	auth string
	http *http.Client
}

func newClient(base, auth string) *client {
	return &client{
		base: base,
		auth: auth,
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *client) do(method, path string, body any, headers map[string]string) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func (c *client) reserve(user, item, key string, qty int) (int, map[string]any, error) {
	code, b, err := c.do(http.MethodPost, "/reservations", map[string]any{
		"itemId": item, "userId": user, "qty": qty,
	}, map[string]string{"Idempotency-Key": key})
	if err != nil {
		return 0, nil, err
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return code, out, nil
}

func (c *client) health() (map[string]any, error) {
	_, b, err := c.do(http.MethodGet, "/health", nil, nil)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

func (c *client) reset() error {
	// Authority first so the API can re-sync shadow counts from a clean stock.
	req, err := http.NewRequest(http.MethodPost, c.auth+"/admin/reset", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	_, _, err = c.do(http.MethodPost, "/admin/reset", map[string]string{}, nil)
	return err
}

func (c *client) setMode(mode string) error {
	req, err := http.NewRequest(http.MethodPost, c.auth+"/admin/mode",
		bytes.NewReader([]byte(fmt.Sprintf(`{"mode":%q}`, mode))))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *client) list(user string) ([]map[string]any, error) {
	_, b, err := c.do(http.MethodGet, "/reservations?userId="+user, nil, nil)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *client) reconcile() error {
	_, _, err := c.do(http.MethodPost, "/admin/reconcile", map[string]string{}, nil)
	return err
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}
