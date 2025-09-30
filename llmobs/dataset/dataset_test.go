// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package dataset

import (
	"math/rand"
	"testing"
)

func BenchmarkDatasetIterator(b *testing.B) {
	b.ReportAllocs()

	records := generateRandomRecords(10000)
	ds := &Dataset{records: records}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for range ds.Records() {
		}
	}
}

func BenchmarkDatasetLoop(b *testing.B) {
	b.ReportAllocs()

	records := generateRandomRecords(10000)
	ds := &Dataset{records: records}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		for range ds.records {
		}
	}
	b.StopTimer()
}

// randomString makes a random alphanumeric string of length n.
func randomString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// randomMap makes a map[string]any with k entries of random strings/ints.
func randomMap(k int) map[string]any {
	m := make(map[string]any, k)
	for i := 0; i < k; i++ {
		key := randomString(5)
		// Randomly assign int or string
		if rand.Intn(2) == 0 {
			m[key] = rand.Intn(1000)
		} else {
			m[key] = randomString(8)
		}
	}
	return m
}

// randomRecord makes a single Record with randomized fields.
func randomRecord() *Record {
	return &Record{
		id:             randomString(10),
		Input:          randomMap(rand.Intn(5) + 1), // 1–5 entries
		ExpectedOutput: rand.Intn(1000),             // simple int for example
		Metadata:       randomMap(rand.Intn(3) + 1), // 1–3 entries
		version:        rand.Intn(10),               // version 0–9
	}
}

// GenerateRandomRecords makes a slice of n random Records.
func generateRandomRecords(n int) []*Record {
	records := make([]*Record, n)
	for i := 0; i < n; i++ {
		records[i] = randomRecord()
	}
	return records
}
