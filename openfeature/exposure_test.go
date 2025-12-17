// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
	"testing"
)

func TestLRUCache_NewEntry(t *testing.T) {
	cache := newExposureLRUCache(100)

	result := cache.add("flag|user", "allocation", "variant")
	if !result {
		t.Error("adding new entry should return true")
	}
}

func TestLRUCache_DuplicateEntry(t *testing.T) {
	cache := newExposureLRUCache(100)

	first := cache.add("flag|user", "allocation", "variant")
	if !first {
		t.Error("first add should return true")
	}

	second := cache.add("flag|user", "allocation", "variant")
	if second {
		t.Error("second add with same values should return false")
	}
}

func TestLRUCache_SameSubject(t *testing.T) {
	cache := newExposureLRUCache(defaultExposureCacheCapacity)

	// Same subject evaluates same flag 5 times
	key := "test-flag|user-123"
	allocation := "default-allocation"
	variant := "variant-a"

	for i := 0; i < 5; i++ {
		result := cache.add(key, allocation, variant)
		if i == 0 && !result {
			t.Error("first add should return true")
		} else if i > 0 && result {
			t.Errorf("add #%d should return false (duplicate)", i+1)
		}
	}
}

func TestLRUCache_DifferentSubjects(t *testing.T) {
	cache := newExposureLRUCache(defaultExposureCacheCapacity)

	// 5 different subjects evaluate same flag
	allocation := "default-allocation"
	variant := "variant-a"

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("test-flag|user-%d", i)
		result := cache.add(key, allocation, variant)
		if !result {
			t.Errorf("add for subject %d should return true (unique subject)", i)
		}
	}
}

func TestLRUCache_VariantCycle(t *testing.T) {
	cache := newExposureLRUCache(defaultExposureCacheCapacity)

	key := "test-flag|user-123"
	allocation := "default-allocation"

	// variant-a -> variant-b -> variant-a (all should be new exposures)
	variants := []string{"variant-a", "variant-b", "variant-a"}

	for i, variant := range variants {
		result := cache.add(key, allocation, variant)
		if !result {
			t.Errorf("variant cycle step %d (%s) should return true", i+1, variant)
		}
	}
}

func TestLRUCache_AllocationCycle(t *testing.T) {
	cache := newExposureLRUCache(defaultExposureCacheCapacity)

	key := "test-flag|user-123"
	variant := "variant-a"

	// allocation-a -> allocation-b -> allocation-a (all should be new exposures)
	allocations := []string{"allocation-a", "allocation-b", "allocation-a"}

	for i, allocation := range allocations {
		result := cache.add(key, allocation, variant)
		if !result {
			t.Errorf("allocation cycle step %d (%s) should return true", i+1, allocation)
		}
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	capacity := 3
	cache := newExposureLRUCache(capacity)

	// Fill cache to capacity: user-0, user-1, user-2
	// Order after: user-2 (front), user-1, user-0 (back/oldest)
	for i := 0; i < capacity; i++ {
		key := fmt.Sprintf("flag|user-%d", i)
		cache.add(key, "alloc", "variant")
	}

	// Add one more to trigger eviction of oldest (user-0)
	// Order after: user-new (front), user-2, user-1 (back/oldest)
	cache.add("flag|user-new", "alloc", "variant")

	// Re-adding evicted entry should return true (it's new again)
	// This also triggers eviction of user-1 (now oldest)
	// Order after: user-0 (front), user-new, user-2 (back/oldest)
	result := cache.add("flag|user-0", "alloc", "variant")
	if !result {
		t.Error("re-adding evicted entry should return true")
	}

	// user-2 should still be in cache (wasn't evicted)
	result = cache.add("flag|user-2", "alloc", "variant")
	if result {
		t.Error("user-2 should still be cached, add should return false")
	}

	// user-1 was evicted when user-0 was re-added
	result = cache.add("flag|user-1", "alloc", "variant")
	if !result {
		t.Error("user-1 should have been evicted")
	}
}

func TestLRUCache_LRUOrdering(t *testing.T) {
	capacity := 3
	cache := newExposureLRUCache(capacity)

	// Add entries: user-0, user-1, user-2
	// LRU order (back=least recent): user-0, user-1, user-2
	cache.add("flag|user-0", "alloc", "variant")
	cache.add("flag|user-1", "alloc", "variant")
	cache.add("flag|user-2", "alloc", "variant")

	// Access user-0 again (moves it to most recently used)
	// LRU order: user-1 (least recent), user-2, user-0 (most recent)
	cache.add("flag|user-0", "alloc", "variant")

	// Add new entry - should evict user-1 (least recently used)
	// LRU order: user-2, user-0, user-new (most recent)
	cache.add("flag|user-new", "alloc", "variant")

	// user-0 should still be cached (was recently accessed)
	result := cache.add("flag|user-0", "alloc", "variant")
	if result {
		t.Error("user-0 should still be cached after recent access")
	}

	// user-2 should still be cached (check BEFORE user-1 to avoid eviction)
	result = cache.add("flag|user-2", "alloc", "variant")
	if result {
		t.Error("user-2 should still be cached")
	}

	// user-1 should have been evicted (was least recently used)
	result = cache.add("flag|user-1", "alloc", "variant")
	if !result {
		t.Error("user-1 should have been evicted")
	}
}

func TestLRUCache_EmptyStrings(t *testing.T) {
	cache := newExposureLRUCache(100)

	// Empty flag key
	result := cache.add("|user", "alloc", "variant")
	if !result {
		t.Error("empty flag key should still be added")
	}

	// Empty subject
	result = cache.add("flag|", "alloc", "variant")
	if !result {
		t.Error("empty subject should still be added")
	}

	// All empty
	result = cache.add("|", "", "")
	if !result {
		t.Error("all empty strings should still be added")
	}

	// Duplicate of all empty should return false
	result = cache.add("|", "", "")
	if result {
		t.Error("duplicate empty entry should return false")
	}
}

func TestLRUCache_ZeroCapacity(t *testing.T) {
	cache := newExposureLRUCache(0)

	// With zero capacity, nothing is cached - every add is "new"
	for i := 0; i < 5; i++ {
		result := cache.add("flag|user", "alloc", "variant")
		if !result {
			t.Errorf("zero capacity cache: add #%d should return true (no caching)", i+1)
		}
	}
}

func TestLRUCache_SingleCapacity(t *testing.T) {
	cache := newExposureLRUCache(1)

	// First add
	result := cache.add("flag|user-1", "alloc", "variant")
	if !result {
		t.Error("first add should return true")
	}

	// Duplicate should return false
	result = cache.add("flag|user-1", "alloc", "variant")
	if result {
		t.Error("duplicate should return false")
	}

	// New entry evicts the old one
	result = cache.add("flag|user-2", "alloc", "variant")
	if !result {
		t.Error("new entry should return true")
	}

	// Old entry was evicted, re-adding returns true
	result = cache.add("flag|user-1", "alloc", "variant")
	if !result {
		t.Error("evicted entry re-added should return true")
	}

	// user-2 was evicted when user-1 was re-added
	result = cache.add("flag|user-2", "alloc", "variant")
	if !result {
		t.Error("user-2 should have been evicted")
	}
}
