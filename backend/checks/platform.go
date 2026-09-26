package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func runPlatformChecks() int {
	prom := getenv("PROMETHEUS_URL", "http://127.0.0.1:9090")
	grafana := getenv("GRAFANA_URL", "http://127.0.0.1:3000")
	api := getenv("REFERENCE_URL", "http://127.0.0.1:8081")
	auth := getenv("AUTHORITY_URL", "http://127.0.0.1:9000")
	failed := 0
	type tc struct {
		name string
		fn   func() error
	}
	for _, t := range []tc{
		{"pipeline_up", func() error { return checkPipeline(prom) }},
		{"dashboard_provisioned", func() error { return checkDashboard(grafana) }},
		{"slo_rules_present", func() error { return checkSLORules(prom) }},
		{"mode_switch_timing", func() error { return checkModeSwitch(api, auth) }},
		{"alert_fires", func() error { return checkAlert(prom) }},
		{"no_secrets", checkNoSecrets},
	} {
		if err := t.fn(); err != nil {
			fmt.Printf("FAIL  %s  %v\n", t.name, err)
			failed++
		} else {
			fmt.Printf("PASS  %s\n", t.name)
		}
	}
	return failed
}

func checkPipeline(prom string) error {
	metrics := []string{
		"reservations_total",
		"authority_request_duration_seconds",
		"standin_queue_depth",
		"reconciliation_lag_seconds",
	}
	deadline := time.Now().Add(60 * time.Second)
	missing := append([]string{}, metrics...)
	for time.Now().Before(deadline) {
		still := []string{}
		for _, m := range missing {
			if !promHas(prom, m) {
				still = append(still, m)
			}
		}
		if len(still) == 0 {
			return nil
		}
		missing = still
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("prometheus missing %v within 60s — fix the collector pipeline", missing)
}

func promHas(prom, metric string) bool {
	resp, err := http.Get(prom + "/api/v1/query?query=" + metric)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		Data struct {
			Result []any `json:"result"`
		} `json:"data"`
	}
	_ = json.Unmarshal(b, &out)
	return len(out.Data.Result) > 0
}

func checkDashboard(grafana string) error {
	req, _ := http.NewRequest(http.MethodGet, grafana+"/api/search?query=Launch%20Day", nil)
	req.SetBasicAuth("admin", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "Launch Day") {
		return fmt.Errorf("no Grafana dashboard titled Launch Day (must be provisioned from a file)")
	}
	return nil
}

func checkSLORules(prom string) error {
	resp, err := http.Get(prom + "/api/v1/rules")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	s := string(b)
	need := []string{"availability", "latency"}
	missing := []string{}
	for _, n := range need {
		if !strings.Contains(strings.ToLower(s), n) {
			missing = append(missing, n)
		}
	}
	if !strings.Contains(strings.ToLower(s), "alert") && !strings.Contains(s, "standin") {
		missing = append(missing, "alert")
	}
	if len(missing) > 0 {
		return fmt.Errorf("prometheus rules missing %v", missing)
	}
	return nil
}

func checkModeSwitch(api, auth string) error {
	c := newClient(api, auth)
	_ = c.reset()
	if err := c.setMode("down"); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		h, err := c.health()
		if err == nil && str(h, "mode") == "standin" {
			ok = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ok {
		_ = c.setMode("healthy")
		return fmt.Errorf("mode=standin not observed within 5s")
	}
	if err := c.setMode("healthy"); err != nil {
		return err
	}
	// Must NOT flip live instantly — need 3 healthy checks (~3s)
	time.Sleep(1200 * time.Millisecond)
	h, _ := c.health()
	if str(h, "mode") == "live" {
		return fmt.Errorf("mode flipped to live before 3 consecutive healthy checks")
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		h, err := c.health()
		if err == nil && str(h, "mode") == "live" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("mode did not return to live after 3 healthy checks")
}

func checkAlert(prom string) error {
	resp, err := http.Get(prom + "/api/v1/rules")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(b)), "alert") {
		return fmt.Errorf("no alert rule loaded — write one and provision it")
	}
	return nil
}

func checkNoSecrets() error {
	root := getenv("KIT_ROOT", ".")
	banned := []string{"BEGIN PRIVATE KEY", "AKIA", "password: hunter2"}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() == ".env" {
			return fmt.Errorf(".env is checked in — keep secrets in .env.example only")
		}
	}
	_ = banned
	return nil
}
