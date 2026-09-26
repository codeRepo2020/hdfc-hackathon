package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

func runBackendChecks(c *client) int {
	tests := []struct {
		name string
		fn   func(*client) error
	}{
		{"idem_replay", checkIdemReplay},
		{"idem_body_mismatch", checkIdemMismatch},
		{"race_single_item", checkRace},
		{"standin_accept", checkStandinAccept},
		{"standin_replay", checkStandinReplay},
		{"standin_no_loss", checkStandinNoLoss},
		{"health_truthful", checkHealthTruthful},
		{"stretch_conflict", checkStretchConflict},
	}
	failed := 0
	for _, t := range tests {
		if err := c.reset(); err != nil {
			fmt.Printf("FAIL  %s  reset: %v\n", t.name, err)
			failed++
			continue
		}
		time.Sleep(200 * time.Millisecond)
		if err := t.fn(c); err != nil {
			fmt.Printf("FAIL  %s  %v\n", t.name, err)
			failed++
		} else {
			fmt.Printf("PASS  %s\n", t.name)
		}
	}
	return failed
}

func checkIdemReplay(c *client) error {
	key := "k-replay"
	user := "u-1"
	var id string
	for i := 0; i < 50; i++ {
		_, out, err := c.reserve(user, "hype-001", key, 1)
		if err != nil {
			return err
		}
		if id == "" {
			id = str(out, "reservationId")
		} else if str(out, "reservationId") != id {
			return fmt.Errorf("sequential replay minted a second reservation")
		}
	}
	var mu sync.Mutex
	ids := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, out, err := c.reserve(user, "hype-001", key, 1)
			if err != nil {
				return
			}
			mu.Lock()
			ids[str(out, "reservationId")]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 1 {
		return fmt.Errorf("concurrent replay produced %d reservation ids", len(ids))
	}
	return nil
}

func checkIdemMismatch(c *client) error {
	key := "k-mismatch"
	_, _, err := c.reserve("u-2", "hype-001", key, 1)
	if err != nil {
		return err
	}
	code, _, err := c.reserve("u-2", "hype-002", key, 1)
	if err != nil {
		return err
	}
	if code != 422 {
		return fmt.Errorf("expected 422, got %d", code)
	}
	return nil
}

func checkRace(c *client) error {
	const n = 200
	var confirmed, rejected atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			<-start
			code, out, err := c.reserve(fmt.Sprintf("u-%d", i+1), "hype-001", fmt.Sprintf("race-%d", i), 1)
			if err != nil {
				return
			}
			st := str(out, "status")
			if code == 201 && st == "confirmed" {
				confirmed.Add(1)
			} else {
				rejected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if confirmed.Load() != 50 || rejected.Load() != 150 {
		return fmt.Errorf("confirmed=%d rejected=%d (want 50/150)", confirmed.Load(), rejected.Load())
	}
	return nil
}

func checkStandinAccept(c *client) error {
	if err := c.setMode("down"); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	pending := 0
	rejected := 0
	for i := 0; i < 15; i++ {
		code, out, err := c.reserve(fmt.Sprintf("u-%d", i+1), "hype-001", fmt.Sprintf("si-%d", i), 1)
		if err != nil {
			return err
		}
		if code == 202 && str(out, "status") == "pending" {
			pending++
		} else {
			rejected++
		}
	}
	_ = c.setMode("healthy")
	if pending != 10 || rejected != 5 {
		return fmt.Errorf("pending=%d rejected=%d (want 10/5)", pending, rejected)
	}
	return nil
}

func checkStandinReplay(c *client) error {
	if err := c.setMode("down"); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	users := []string{}
	accepted := 0
	for i := 0; i < 10; i++ {
		u := fmt.Sprintf("u-%d", 20+i)
		users = append(users, u)
		code, out, err := c.reserve(u, "hype-001", fmt.Sprintf("sr-%d", i), 1)
		if err != nil {
			return err
		}
		if code == 202 || str(out, "status") == "pending" {
			accepted++
		}
	}
	if accepted == 0 {
		return fmt.Errorf("no pending reservations accepted while authority was down")
	}
	if err := c.setMode("healthy"); err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		pending := 0
		for _, u := range users {
			list, err := c.list(u)
			if err != nil {
				return err
			}
			for _, r := range list {
				if str(r, "status") == "pending" {
					pending++
				}
			}
		}
		if pending == 0 {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("reservations still pending after replay window")
}

func checkStandinNoLoss(c *client) error {
	if os.Getenv("SKIP_PROCESS_KILL") == "1" {
		return nil
	}
	if err := c.setMode("down"); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	for i := 0; i < 8; i++ {
		_, _, _ = c.reserve(fmt.Sprintf("u-%d", 40+i), "hype-001", fmt.Sprintf("nl-%d", i), 1)
	}
	compose := os.Getenv("COMPOSE")
	if compose == "" {
		compose = "docker compose"
	}
	svc := getenv("KILL_SERVICE", "reservation-api")
	_ = exec.Command("sh", "-c", compose+" kill "+svc).Run()
	time.Sleep(500 * time.Millisecond)
	_ = exec.Command("sh", "-c", compose+" start "+svc).Run()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.health(); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = c.setMode("healthy")
	time.Sleep(3 * time.Second)
	_ = c.reconcile()
	time.Sleep(2 * time.Second)
	pending := 0
	seen := 0
	for i := 0; i < 8; i++ {
		list, err := c.list(fmt.Sprintf("u-%d", 40+i))
		if err != nil {
			return err
		}
		for _, r := range list {
			seen++
			if str(r, "status") == "pending" {
				pending++
			}
		}
	}
	if seen == 0 {
		return fmt.Errorf("all queued reservations lost after restart")
	}
	if pending > 0 {
		return fmt.Errorf("%d reservations still pending after restart", pending)
	}
	return nil
}

func checkHealthTruthful(c *client) error {
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
		return fmt.Errorf("/health did not report mode=standin within 5s")
	}
	if err := c.setMode("healthy"); err != nil {
		return err
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		h, err := c.health()
		if err == nil && str(h, "mode") == "live" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("/health did not return to mode=live")
}

func checkStretchConflict(c *client) error {
	if err := c.setMode("down"); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	for i := 0; i < 6; i++ {
		_, _, _ = c.reserve(fmt.Sprintf("u-%d", 80+i), "hype-001", fmt.Sprintf("cf-%d", i), 1)
	}
	if err := c.setMode("conflicting"); err != nil {
		return err
	}
	time.Sleep(4 * time.Second)
	_ = c.reconcile()
	time.Sleep(1 * time.Second)
	reversed := 0
	for i := 0; i < 6; i++ {
		list, _ := c.list(fmt.Sprintf("u-%d", 80+i))
		for _, r := range list {
			if str(r, "status") == "reversed" {
				reversed++
			}
		}
	}
	if reversed == 0 {
		return fmt.Errorf("expected reversed reservations under conflicting mode (stretch)")
	}
	return nil
}
