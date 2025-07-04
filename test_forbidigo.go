package main

import (
	"os"
)

func main() {
	// This should trigger our forbidigo rule
	value := os.Getenv("TEST_VAR")
	_ = value

	// This should also trigger our forbidigo rule
	value2, exists := os.LookupEnv("TEST_VAR2")
	if exists {
		_ = value2
	}
}
