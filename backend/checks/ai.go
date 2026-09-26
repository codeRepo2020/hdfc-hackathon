package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type aiEvent struct {
	EventID       string `json:"eventId"`
	TS            string `json:"ts"`
	Type          string `json:"type"`
	ItemID        string `json:"itemId"`
	UserID        string `json:"userId"`
	ReservationID string `json:"reservationId"`
	Qty           int    `json:"qty"`
	Available     *int   `json:"available"`
	DeviceID      string `json:"deviceId"`
	IP            string `json:"ip"`
	Mode          string `json:"mode"`
	Reason        string `json:"reason"`
	Status        string `json:"status"`
	AboutUserID   string `json:"aboutUserId"`
}

type aiQuestion struct {
	QuestionID  string `json:"questionId"`
	AskedAt     string `json:"askedAt"`
	UserID      string `json:"userId"`
	Text        string `json:"text"`
	ItemID      string `json:"itemId"`
	AboutUserID string `json:"aboutUserId"`
}

type aiAnswer struct {
	QuestionID string          `json:"questionId"`
	Intent     string          `json:"intent"`
	Answerable bool            `json:"answerable"`
	Text       string          `json:"text"`
	Value      json.RawMessage `json:"value"`
	Freshness  string          `json:"freshness"`
	Citations  []string        `json:"citations"`
}

type aiScore struct {
	UserID  string   `json:"userId"`
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons"`
}

func runAIChecks() int {
	root := getenv("KIT_ROOT", ".")
	failed := 0
	if err := runAIScript(root); err != nil {
		fmt.Printf("FAIL  run  %v\n", err)
		return 1
	}
	events, err := readAIEvents(filepath.Join(root, "ai", "data", "events.jsonl"))
	if err != nil {
		fmt.Printf("FAIL  data  events: %v\n", err)
		return 1
	}
	questions, err := readAIQuestions(filepath.Join(root, "ai", "data", "questions.jsonl"))
	if err != nil {
		fmt.Printf("FAIL  data  questions: %v\n", err)
		return 1
	}
	answers, err := readAIAnswers(filepath.Join(root, "ai", "out", "answers.jsonl"))
	if err != nil {
		fmt.Printf("FAIL  answers_complete  %v\n", err)
		return 1
	}
	scores, err := readAIScores(filepath.Join(root, "ai", "out", "scores.jsonl"))
	if err != nil {
		fmt.Printf("FAIL  ranker_complete  %v\n", err)
		return 1
	}

	byQ := map[string]aiAnswer{}
	for _, a := range answers {
		byQ[a.QuestionID] = a
	}
	if msg := checkAnswersComplete(questions, byQ); msg != "" {
		fmt.Printf("FAIL  answers_complete  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  answers_complete\n")
	}
	if msg := checkIntentsObvious(questions, byQ); msg != "" {
		fmt.Printf("FAIL  intents_obvious  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  intents_obvious\n")
	}
	if msg := checkNoFutureLeak(questions, byQ, events); msg != "" {
		fmt.Printf("FAIL  no_future_leak  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  no_future_leak\n")
	}
	if msg := checkNoInventedStock(questions, byQ, events); msg != "" {
		fmt.Printf("FAIL  no_invented_stock  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  no_invented_stock\n")
	}
	if msg := checkUnknownWhenAbsent(questions, byQ, events); msg != "" {
		fmt.Printf("FAIL  unknown_when_absent  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  unknown_when_absent\n")
	}
	if msg := checkStatusAndPending(questions, byQ, events); msg != "" {
		fmt.Printf("FAIL  status_and_pending  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  status_and_pending\n")
	}
	if msg := checkRankerComplete(events, scores); msg != "" {
		fmt.Printf("FAIL  ranker_complete  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  ranker_complete\n")
	}
	if msg := checkRankerSeparates(events, scores); msg != "" {
		fmt.Printf("FAIL  ranker_separates  %s\n", msg)
		failed++
	} else {
		fmt.Printf("PASS  ranker_separates\n")
	}

	ans1 := filepath.Join(root, "ai", "out", "answers.jsonl")
	sco1 := filepath.Join(root, "ai", "out", "scores.jsonl")
	aBytes, _ := os.ReadFile(ans1)
	sBytes, _ := os.ReadFile(sco1)
	if err := runAIScript(root); err != nil {
		fmt.Printf("FAIL  deterministic  second run: %v\n", err)
		failed++
	} else {
		a2, _ := os.ReadFile(ans1)
		s2, _ := os.ReadFile(sco1)
		if string(aBytes) != string(a2) || string(sBytes) != string(s2) {
			fmt.Printf("FAIL  deterministic  second run wrote different files\n")
			failed++
		} else {
			fmt.Printf("PASS  deterministic\n")
		}
	}
	return failed
}

func runAIScript(root string) error {
	script := filepath.Join(root, "ai", "run.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = filepath.Join(root, "ai")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func checkAnswersComplete(questions []aiQuestion, byQ map[string]aiAnswer) string {
	if len(byQ) != len(questions) {
		return fmt.Sprintf("got %d answers, want %d", len(byQ), len(questions))
	}
	allowed := map[string]bool{
		"stock_now": true, "my_status": true, "why_reversed": true,
		"count_pending": true, "policy": true, "other": true,
	}
	for _, q := range questions {
		a, ok := byQ[q.QuestionID]
		if !ok {
			return "missing " + q.QuestionID
		}
		if !allowed[a.Intent] {
			return q.QuestionID + " has unknown intent " + a.Intent
		}
		if strings.TrimSpace(a.Text) == "" {
			return q.QuestionID + " has empty text"
		}
		if a.Freshness == "" {
			return q.QuestionID + " missing freshness"
		}
	}
	return ""
}

func obviousIntent(q aiQuestion) (string, bool) {
	text := strings.ToLower(q.Text)
	if q.AboutUserID != "" && q.AboutUserID != q.UserID {
		return "other", true
	}
	if strings.Contains(text, "next drop") || strings.Contains(text, "colour") || strings.Contains(text, "color") {
		return "other", true
	}
	if strings.Contains(text, "can i") || strings.Contains(text, "what happens if i release") {
		return "policy", true
	}
	if strings.Contains(text, "pending") || strings.Contains(text, "waiting") || strings.Contains(text, "in line") {
		return "count_pending", true
	}
	if strings.Contains(text, "reverse") || strings.Contains(text, "bounce") {
		return "why_reversed", true
	}
	if strings.Contains(text, "how many") && strings.Contains(text, "left") {
		return "stock_now", true
	}
	if strings.Contains(text, "did i get") || strings.Contains(text, "my reservation") || strings.Contains(text, "my spot") {
		return "my_status", true
	}
	return "", false
}

func checkIntentsObvious(questions []aiQuestion, byQ map[string]aiAnswer) string {
	ok, n := 0, 0
	var miss []string
	for _, q := range questions {
		want, known := obviousIntent(q)
		if !known {
			continue
		}
		n++
		if byQ[q.QuestionID].Intent == want {
			ok++
		} else if len(miss) < 4 {
			miss = append(miss, fmt.Sprintf("%s got %s want %s", q.QuestionID, byQ[q.QuestionID].Intent, want))
		}
	}
	if n == 0 {
		return "no obvious questions (kit data missing?)"
	}
	if ok*100/n < 80 {
		return fmt.Sprintf("%d/%d obvious intents correct (need ≥ 80%%); e.g. %s", ok, n, strings.Join(miss, "; "))
	}
	return ""
}

func checkNoFutureLeak(questions []aiQuestion, byQ map[string]aiAnswer, events []aiEvent) string {
	byID := map[string]aiEvent{}
	for _, e := range events {
		byID[e.EventID] = e
	}
	for _, q := range questions {
		a := byQ[q.QuestionID]
		asked, err := parseAITS(q.AskedAt)
		if err != nil {
			return q.QuestionID + " bad askedAt"
		}
		for _, id := range a.Citations {
			if id == "policy" {
				continue
			}
			e, ok := byID[id]
			if !ok {
				return q.QuestionID + " cites unknown event " + id
			}
			ts, err := parseAITS(e.TS)
			if err != nil || ts.After(asked) {
				return q.QuestionID + " cites future or unparsable " + id
			}
		}
	}
	return ""
}

func checkNoInventedStock(questions []aiQuestion, byQ map[string]aiAnswer, events []aiEvent) string {
	for _, q := range questions {
		a := byQ[q.QuestionID]
		wantIntent, known := obviousIntent(q)
		if a.Intent != "stock_now" && !(known && wantIntent == "stock_now") {
			continue
		}
		asked, _ := parseAITS(q.AskedAt)
		want, ok := reconstructStock(events, q.ItemID, asked)
		if !ok {
			if a.Answerable {
				return q.QuestionID + " should not be answerable (no snapshot)"
			}
			if valueIsNumber(a.Value) {
				return q.QuestionID + " invented a number with no snapshot"
			}
			continue
		}
		if !a.Answerable {
			return q.QuestionID + " stock is reconstructable but answerable=false"
		}
		got, isNum := numberValue(a.Value)
		if !isNum || got != want {
			return fmt.Sprintf("%s stock value=%v want %d", q.QuestionID, rawPreview(a.Value), want)
		}
	}
	return ""
}

func checkUnknownWhenAbsent(questions []aiQuestion, byQ map[string]aiAnswer, events []aiEvent) string {
	for _, q := range questions {
		a := byQ[q.QuestionID]
		text := strings.ToLower(q.Text)
		mustUnknown := false
		if q.AboutUserID != "" && q.AboutUserID != q.UserID {
			mustUnknown = true
		}
		if strings.Contains(text, "next drop") || strings.Contains(text, "colour") || strings.Contains(text, "color") {
			mustUnknown = true
		}
		if q.ItemID == "hype-999" {
			mustUnknown = true
		}
		if strings.Contains(text, "reverse") && q.ItemID != "" {
			asked, _ := parseAITS(q.AskedAt)
			want, known := obviousIntent(q)
			if known && want == "why_reversed" && reversedReason(events, q.UserID, q.ItemID, asked) == "" {
				mustUnknown = true
			}
		}
		if !mustUnknown {
			continue
		}
		if a.Answerable {
			return q.QuestionID + " must not be answerable"
		}
		if valueIsNumber(a.Value) {
			return q.QuestionID + " must not invent a number"
		}
	}
	return ""
}

func checkStatusAndPending(questions []aiQuestion, byQ map[string]aiAnswer, events []aiEvent) string {
	for _, q := range questions {
		a := byQ[q.QuestionID]
		asked, _ := parseAITS(q.AskedAt)
		wantIntent, known := obviousIntent(q)
		if !known {
			continue
		}
		if wantIntent == "my_status" {
			if !a.Answerable {
				return q.QuestionID + " my_status should be answerable"
			}
			want := latestUserItemStatus(events, q.UserID, q.ItemID, asked)
			got := strings.Trim(strings.ToLower(stringValue(a.Value)), `"`)
			if got != want {
				return fmt.Sprintf("%s my_status value=%q want %q", q.QuestionID, got, want)
			}
		}
		if wantIntent == "count_pending" {
			if !a.Answerable {
				return q.QuestionID + " count_pending should be answerable"
			}
			want := pendingCount(events, q.ItemID, asked)
			got, isNum := numberValue(a.Value)
			if !isNum || got != want {
				return fmt.Sprintf("%s pending value=%v want %d", q.QuestionID, rawPreview(a.Value), want)
			}
		}
	}
	return ""
}

func checkRankerComplete(events []aiEvent, scores []aiScore) string {
	need := map[string]bool{}
	for _, e := range events {
		if e.UserID != "" {
			need[e.UserID] = true
		}
	}
	seen := map[string]bool{}
	for _, s := range scores {
		if s.Score < 0 || s.Score > 1 || math.IsNaN(s.Score) {
			return s.UserID + " score out of [0,1]"
		}
		seen[s.UserID] = true
	}
	if len(seen) != len(need) {
		return fmt.Sprintf("scored %d users, log has %d", len(seen), len(need))
	}
	for u := range need {
		if !seen[u] {
			return "missing score for " + u
		}
	}
	return ""
}

func checkRankerSeparates(events []aiEvent, scores []aiScore) string {
	byUser := map[string]float64{}
	for _, s := range scores {
		byUser[s.UserID] = s.Score
	}
	high, low := gamingSignalUsers(events)
	if len(high) < 8 || len(low) < 8 {
		return fmt.Sprintf("not enough signal users high=%d low=%d (kit data?)", len(high), len(low))
	}
	hm, lm := meanScore(high, byUser), meanScore(low, byUser)
	if hm < lm+0.15 {
		return fmt.Sprintf("high-signal mean=%.3f low-signal mean=%.3f (need high ≥ low+0.15)", hm, lm)
	}
	return ""
}

func gamingSignalUsers(events []aiEvent) (high, low []string) {
	type stats struct {
		confirms, releases int
		holds              []float64
		device             string
	}
	st := map[string]*stats{}
	deviceUsers := map[string]map[string]bool{}
	confirmTS := map[string]time.Time{}
	stat := func(u string) *stats {
		if st[u] == nil {
			st[u] = &stats{}
		}
		return st[u]
	}
	for _, e := range events {
		if e.UserID == "" {
			continue
		}
		s := stat(e.UserID)
		if e.DeviceID != "" {
			s.device = e.DeviceID
			if deviceUsers[e.DeviceID] == nil {
				deviceUsers[e.DeviceID] = map[string]bool{}
			}
			deviceUsers[e.DeviceID][e.UserID] = true
		}
		ts, _ := parseAITS(e.TS)
		switch e.Type {
		case "reservation_confirmed":
			s.confirms++
			if e.ReservationID != "" {
				confirmTS[e.ReservationID] = ts
			}
		case "reservation_released":
			s.releases++
			if t0, ok := confirmTS[e.ReservationID]; ok {
				s.holds = append(s.holds, ts.Sub(t0).Seconds())
			}
		}
	}
	shared := map[string]bool{}
	for _, users := range deviceUsers {
		if len(users) >= 4 {
			for u := range users {
				shared[u] = true
			}
		}
	}
	for u, s := range st {
		if s.releases >= 4 && medianFloat(s.holds) > 0 && medianFloat(s.holds) < 20 {
			high = append(high, u)
			continue
		}
		if shared[u] {
			high = append(high, u)
			continue
		}
		if s.confirms == 1 && s.releases == 0 && s.device != "" && len(deviceUsers[s.device]) == 1 {
			low = append(low, u)
		}
	}
	sort.Strings(high)
	sort.Strings(low)
	return high, low
}

func meanScore(users []string, byUser map[string]float64) float64 {
	if len(users) == 0 {
		return 0
	}
	sum := 0.0
	n := 0
	for _, u := range users {
		if v, ok := byUser[u]; ok {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func medianFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func reconstructStock(events []aiEvent, item string, at time.Time) (int, bool) {
	var last *aiEvent
	for i := range events {
		e := &events[i]
		ts, err := parseAITS(e.TS)
		if err != nil || ts.After(at) || e.Type != "stock_snapshot" || e.ItemID != item {
			continue
		}
		last = e
	}
	if last == nil || last.Available == nil {
		return 0, false
	}
	avail := *last.Available
	lastTS, _ := parseAITS(last.TS)
	for _, e := range events {
		ts, err := parseAITS(e.TS)
		if err != nil || ts.After(at) || !ts.After(lastTS) || e.ItemID != item {
			continue
		}
		qty := e.Qty
		if qty == 0 {
			qty = 1
		}
		switch e.Type {
		case "reservation_confirmed":
			avail -= qty
		case "reservation_released", "reservation_reversed":
			avail += qty
		}
	}
	return avail, true
}

func latestUserItemStatus(events []aiEvent, user, item string, at time.Time) string {
	latestTS := map[string]time.Time{}
	for _, e := range events {
		if e.UserID != user || e.ItemID != item || e.ReservationID == "" {
			continue
		}
		ts, err := parseAITS(e.TS)
		if err != nil || ts.After(at) {
			continue
		}
		if ts.After(latestTS[e.ReservationID]) {
			latestTS[e.ReservationID] = ts
		}
	}
	if len(latestTS) == 0 {
		return "none"
	}
	var rid string
	var best time.Time
	for id, ts := range latestTS {
		if ts.After(best) {
			best = ts
			rid = id
		}
	}
	st, _ := statusAt(events, rid, at)
	if st == "" {
		return "none"
	}
	return st
}

func statusAt(events []aiEvent, reservationID string, at time.Time) (string, string) {
	var latest *aiEvent
	for i := range events {
		e := &events[i]
		if e.ReservationID != reservationID {
			continue
		}
		ts, err := parseAITS(e.TS)
		if err != nil || ts.After(at) {
			continue
		}
		latest = e
	}
	if latest == NoneEvent() {
		return "", ""
	}
	switch latest.Type {
	case "reservation_created", "reservation_pending":
		return "pending", latest.EventID
	case "reservation_confirmed":
		return "confirmed", latest.EventID
	case "reservation_rejected":
		return "rejected", latest.EventID
	case "reservation_reversed":
		return "reversed", latest.EventID
	case "reservation_released":
		return "released", latest.EventID
	default:
		return latest.Status, latest.EventID
	}
}

func NoneEvent() *aiEvent { return nil }

func pendingCount(events []aiEvent, item string, at time.Time) int {
	rids := map[string]bool{}
	for _, e := range events {
		if e.ItemID == item && e.ReservationID != "" {
			rids[e.ReservationID] = true
		}
	}
	n := 0
	for rid := range rids {
		st, _ := statusAt(events, rid, at)
		if st == "pending" {
			n++
		}
	}
	return n
}

func reversedReason(events []aiEvent, user, item string, at time.Time) string {
	reason := ""
	for _, e := range events {
		if e.Type != "reservation_reversed" || e.UserID != user || e.ItemID != item {
			continue
		}
		ts, err := parseAITS(e.TS)
		if err != nil || ts.After(at) {
			continue
		}
		reason = e.Reason
	}
	return reason
}

func parseAITS(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func numberValue(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return int(math.Round(n)), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err == nil {
			return v, true
		}
	}
	return 0, false
}

func valueIsNumber(raw json.RawMessage) bool {
	_, ok := numberValue(raw)
	return ok
}

func stringValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func rawPreview(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "∅"
	}
	return string(raw)
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row T
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		rows = append(rows, row)
	}
	return rows, sc.Err()
}

func readAIEvents(path string) ([]aiEvent, error)       { return readJSONL[aiEvent](path) }
func readAIQuestions(path string) ([]aiQuestion, error) { return readJSONL[aiQuestion](path) }
func readAIAnswers(path string) ([]aiAnswer, error)     { return readJSONL[aiAnswer](path) }
func readAIScores(path string) ([]aiScore, error)       { return readJSONL[aiScore](path) }
