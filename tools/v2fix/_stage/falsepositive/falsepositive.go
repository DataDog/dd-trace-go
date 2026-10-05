// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// This test verifies that functions with the same names as dd-trace-go v1 functions
// but from different packages are NOT flagged for migration.
package main

// Local type and function definitions that shadow dd-trace-go names
type Span struct {
	Name string
}

func WithServiceName(name string) string {
	return name
}

func TraceID() uint64 {
	return 0
}

func WithDogstatsdAddress(addr string) string {
	return addr
}

func ServiceRule(service string, rate float64) string {
	return service
}

func main() {
	// These should NOT be flagged - they're local functions, not from dd-trace-go v1
	_ = WithServiceName("test")
	_ = TraceID()
	_ = WithDogstatsdAddress("localhost:8125")
	_ = ServiceRule("myservice", 0.5)

	// Local type should NOT be flagged
	var _ Span
}
