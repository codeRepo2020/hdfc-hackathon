package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runSDETChecks() int {
	root := getenv("KIT_ROOT", ".")
	lang := getenv("SDET_LANG", "go")
	failed := 0

	if err := runStudentSuite(root, lang, "reference"); err != nil {
		fmt.Printf("FAIL  passes_reference  %v\n", err)
		failed++
	} else {
		fmt.Printf("PASS  passes_reference\n")
	}

	caught, err := countBuggyFails(root, lang)
	if err != nil {
		fmt.Printf("FAIL  catches_buggy  %v\n", err)
		failed++
	} else if caught < 3 {
		fmt.Printf("FAIL  catches_buggy  found %d distinct defects (need ≥ 3)\n", caught)
		failed++
	} else {
		fmt.Printf("PASS  catches_buggy  (%d defects)\n", caught)
	}

	report := filepath.Join(root, "report", "invariants.md")
	if _, err := os.Stat(report); err != nil {
		fmt.Printf("FAIL  report_present  %s missing\n", report)
		failed++
	} else {
		b, _ := os.ReadFile(report)
		need := []string{"I1", "I2", "I3", "I4", "I5", "I6"}
		ok := true
		for _, n := range need {
			if !strings.Contains(string(b), n) {
				ok = false
			}
		}
		if !ok {
			fmt.Printf("FAIL  report_present  report does not list I1–I6\n")
			failed++
		} else {
			fmt.Printf("PASS  report_present\n")
		}
	}
	return failed
}

func runStudentSuite(root, lang, target string) error {
	start := time.Now()
	cmd, err := suiteCmd(root, lang, target)
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if time.Since(start) > 4*time.Minute {
		return fmt.Errorf("runtime_budget: suite took %s", time.Since(start).Round(time.Second))
	}
	return nil
}

func countBuggyFails(root, lang string) (int, error) {
	cmd, err := suiteCmd(root, lang, "buggy")
	if err != nil {
		return 0, err
	}
	out, _ := cmd.CombinedOutput()
	s := string(out)
	// Count distinct I-fail markers the student suite is expected to print.
	n := 0
	for _, id := range []string{"I1", "I2", "I3", "I4"} {
		if strings.Contains(s, "FAIL") && strings.Contains(s, id) {
			n++
		}
	}
	report := filepath.Join(root, "report", "invariants.md")
	if b, err := os.ReadFile(report); err == nil {
		n = 0
		for _, id := range []string{"I1", "I2", "I3", "I4", "I5", "I6"} {
			if strings.Contains(string(b), id) && strings.Contains(strings.ToUpper(string(b)), "FAIL") {
				// crude: look for "I1 ... FAIL" on a line
				for _, line := range strings.Split(string(b), "\n") {
					if strings.Contains(line, id) && strings.Contains(strings.ToUpper(line), "FAIL") {
						n++
						break
					}
				}
			}
		}
	}
	_ = s
	return n, nil
}

func suiteCmd(root, lang, target string) (*exec.Cmd, error) {
	base := map[string]string{
		"reference": getenv("REFERENCE_URL", "http://127.0.0.1:8081"),
		"buggy":     getenv("BUGGY_URL", "http://127.0.0.1:8082"),
	}[target]
	auth := getenv("AUTHORITY_URL", "http://127.0.0.1:9000")
	env := append(os.Environ(),
		"API_URL="+base,
		"AUTHORITY_URL="+auth,
		"TARGET="+target,
	)
	switch lang {
	case "go":
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = filepath.Join(root, "tests", "go")
		cmd.Env = env
		return cmd, nil
	case "java":
		cmd := exec.Command("mvn", "-q", "test")
		cmd.Dir = filepath.Join(root, "tests", "java")
		cmd.Env = env
		return cmd, nil
	case "kotlin":
		cmd := exec.Command("mvn", "-q", "test")
		cmd.Dir = filepath.Join(root, "tests", "kotlin")
		cmd.Env = env
		return cmd, nil
	default:
		return nil, fmt.Errorf("unknown SDET_LANG %s", lang)
	}
}
