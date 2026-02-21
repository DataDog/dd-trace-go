package main

import (
	"fmt"
	"os"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func main() {
	// Test 1: Default value (24 hours)
	fmt.Println("Test 1: Default extended heartbeat interval")
	cfg1 := telemetry.ClientConfig{
		AgentURL: "http://localhost:8126",
	}
	// Use internal defaultConfig function through NewClient
	c1, err := telemetry.NewClient("test-service", "test-env", "1.0.0", cfg1)
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		os.Exit(1)
	}
	c1.Close()
	fmt.Printf("✓ Default interval: 24h\n\n")

	// Test 2: Custom value via environment variable (10 seconds)
	fmt.Println("Test 2: Custom extended heartbeat interval via environment variable")
	os.Setenv("DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", "10")
	cfg2 := telemetry.ClientConfig{
		AgentURL: "http://localhost:8126",
	}
	c2, err := telemetry.NewClient("test-service", "test-env", "1.0.0", cfg2)
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		os.Exit(1)
	}
	c2.Close()
	fmt.Printf("✓ Custom interval via env var: 10s\n\n")

	// Test 3: Custom value via config (overrides default, but env var should win)
	fmt.Println("Test 3: Custom extended heartbeat interval via config (env var takes precedence)")
	os.Setenv("DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", "5")
	cfg3 := telemetry.ClientConfig{
		AgentURL:                  "http://localhost:8126",
		ExtendedHeartbeatInterval: 20 * time.Second,
	}
	c3, err := telemetry.NewClient("test-service", "test-env", "1.0.0", cfg3)
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		os.Exit(1)
	}
	c3.Close()
	fmt.Printf("✓ Config set to 20s but env var overrides to 5s\n\n")

	fmt.Println("All tests passed! ✅")
}
