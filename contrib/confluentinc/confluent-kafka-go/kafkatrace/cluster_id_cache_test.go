// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeBootstrapServers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"single server", "broker1:9092", "broker1:9092"},
		{"multiple servers sorted", "a:9092,b:9092,c:9092", "a:9092,b:9092,c:9092"},
		{"multiple servers unsorted", "c:9092,a:9092,b:9092", "a:9092,b:9092,c:9092"},
		{"with whitespace", " broker1:9092 , broker2:9092 ", "broker1:9092,broker2:9092"},
		{"empty string", "", ""},
		{"only commas", ",,", ""},
		{"mixed whitespace and empty", " a:9092 ,, b:9092 ,", "a:9092,b:9092"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeBootstrapServers(tt.input))
		})
	}
}

func TestClusterIDCache(t *testing.T) {
	t.Cleanup(ResetClusterIDCache)

	t.Run("miss returns false", func(t *testing.T) {
		_, ok := GetCachedClusterID("broker1:9092")
		assert.False(t, ok)
	})

	t.Run("hit after set", func(t *testing.T) {
		SetCachedClusterID("broker1:9092", "cluster-abc")
		id, ok := GetCachedClusterID("broker1:9092")
		assert.True(t, ok)
		assert.Equal(t, "cluster-abc", id)
	})

	t.Run("empty key is no-op", func(t *testing.T) {
		SetCachedClusterID("", "cluster-xyz")
		_, ok := GetCachedClusterID("")
		assert.False(t, ok)
	})

	t.Run("empty value is no-op", func(t *testing.T) {
		SetCachedClusterID("broker2:9092", "")
		_, ok := GetCachedClusterID("broker2:9092")
		assert.False(t, ok)
	})
}
