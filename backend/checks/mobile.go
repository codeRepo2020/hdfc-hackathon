package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func runMobileChecks() int {
	root := getenv("KIT_ROOT", ".")
	platform := getenv("MOBILE_PLATFORM", "flutter")
	failed := 0
	switch platform {
	case "flutter":
		if _, err := exec.LookPath("flutter"); err != nil {
			if _, err := exec.LookPath("dart"); err != nil {
				fmt.Println("SKIP  mobile  flutter/dart not installed — unit tests live in mobile/flutter/test")
				return 0
			}
		}
		cmd := exec.Command("flutter", "test")
		if _, err := exec.LookPath("flutter"); err != nil {
			cmd = exec.Command("dart", "test")
		}
		cmd.Dir = filepath.Join(root, "mobile", "flutter")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("FAIL  flutter_tests  %v\n", err)
			failed++
		} else {
			fmt.Printf("PASS  flutter_tests\n")
		}
	case "android":
		cmd := exec.Command("./gradlew", ":state:test")
		cmd.Dir = filepath.Join(root, "mobile", "android")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("FAIL  android_tests  %v\n", err)
			failed++
		} else {
			fmt.Printf("PASS  android_tests\n")
		}
	default:
		fmt.Printf("SKIP  mobile  platform %s — open the project in Xcode / Android Studio\n", platform)
	}
	return failed
}
