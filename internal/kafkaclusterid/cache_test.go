// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkaclusterid

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

func TestNormalizeBootstrapServersList(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{"single server", []string{"broker1:9092"}, "broker1:9092"},
		{"multiple servers unsorted", []string{"c:9092", "a:9092", "b:9092"}, "a:9092,b:9092,c:9092"},
		{"with whitespace", []string{" broker1:9092 ", " broker2:9092 "}, "broker1:9092,broker2:9092"},
		{"empty list", nil, ""},
		{"empty entries", []string{"", " ", "a:9092"}, "a:9092"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeBootstrapServersList(tt.input))
		})
	}
}

func TestClusterIDCache(t *testing.T) {
	t.Cleanup(ResetCache)

	t.Run("miss returns false", func(t *testing.T) {
		_, ok := GetCachedID("broker1:9092")
		assert.False(t, ok)
	})

	t.Run("hit after set", func(t *testing.T) {
		SetCachedID("broker1:9092", "cluster-abc")
		id, ok := GetCachedID("broker1:9092")
		assert.True(t, ok)
		assert.Equal(t, "cluster-abc", id)
	})

	t.Run("empty key is no-op", func(t *testing.T) {
		SetCachedID("", "cluster-xyz")
		_, ok := GetCachedID("")
		assert.False(t, ok)
	})

	t.Run("empty value is no-op", func(t *testing.T) {
		SetCachedID("broker2:9092", "")
		_, ok := GetCachedID("broker2:9092")
		assert.False(t, ok)
	})
}
