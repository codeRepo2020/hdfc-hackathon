package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	track := strings.ToLower(getenv("TRACK", "smoke"))
	api := getenv("API_URL", "http://127.0.0.1:8080")
	auth := getenv("AUTHORITY_URL", "http://127.0.0.1:9000")
	c := newClient(api, auth)

	fmt.Printf("== Launch Day checks  track=%s  api=%s ==\n", track, api)
	failed := 0
	switch track {
	case "backend":
		failed = runBackendChecks(c)
	case "platform":
		failed = runPlatformChecks()
	case "sdet":
		failed = runSDETChecks()
	case "mobile":
		failed = runMobileChecks()
	case "ai":
		failed = runAIChecks()
	default:
		failed = runSmoke(c)
	}
	if failed > 0 {
		fmt.Printf("\n%d check(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}

func runSmoke(c *client) int {
	h, err := c.health()
	if err != nil {
		fmt.Printf("FAIL  smoke  api health: %v\n", err)
		return 1
	}
	fmt.Printf("PASS  smoke  api health mode=%s\n", str(h, "mode"))
	return 0
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
