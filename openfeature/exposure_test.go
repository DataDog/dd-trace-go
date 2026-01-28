// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"fmt"
	"sync"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
)

func TestLRUCache_NewEntry(t *testing.T) {
	cache := newExposureLRUCache(100)

	key := exposureCacheKey{flagKey: "flag", targetingKey: "user"}
	value := exposureCacheValue{allocationKey: "allocation", variant: "variant"}
	result := cache.add(key, value)
	if !result {
		t.Error("adding new entry should return true")
	}
}

func TestLRUCache_DuplicateEntry(t *testing.T) {
	cache := newExposureLRUCache(100)

	key := exposureCacheKey{flagKey: "flag", targetingKey: "user"}
	value := exposureCacheValue{allocationKey: "allocation", variant: "variant"}
	first := cache.add(key, value)
	if !first {
		t.Error("first add should return true")
	}

	second := cache.add(key, value)
	if second {
		t.Error("second add with same values should return false")
	}
}

func TestLRUCache_SameSubject(t *testing.T) {
	cache := newExposureLRUCache(defaultExposureCacheCapacity)

	// Same subject evaluates same flag 5 times
	key := exposureCacheKey{flagKey: "test-flag", targetingKey: "user-123"}
	value := exposureCacheValue{allocationKey: "default-allocation", variant: "variant-a"}

	for i := 0; i < 5; i++ {
		result := cache.add(key, value)
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
	value := exposureCacheValue{allocationKey: "default-allocation", variant: "variant-a"}

	for i := 0; i < 5; i++ {
		key := exposureCacheKey{flagKey: "test-flag", targetingKey: fmt.Sprintf("user-%d", i)}
		result := cache.add(key, value)
		if !result {
			t.Errorf("add for subject %d should return true (unique subject)", i)
		}
	}
}

func TestLRUCache_VariantCycle(t *testing.T) {
	cache := newExposureLRUCache(defaultExposureCacheCapacity)

	key := exposureCacheKey{flagKey: "test-flag", targetingKey: "user-123"}

	// variant-a -> variant-b -> variant-a (all should be new exposures)
	variants := []string{"variant-a", "variant-b", "variant-a"}

	for i, variant := range variants {
		value := exposureCacheValue{allocationKey: "default-allocation", variant: variant}
		result := cache.add(key, value)
		if !result {
			t.Errorf("variant cycle step %d (%s) should return true", i+1, variant)
		}
	}
}

func TestLRUCache_AllocationCycle(t *testing.T) {
	cache := newExposureLRUCache(defaultExposureCacheCapacity)

	key := exposureCacheKey{flagKey: "test-flag", targetingKey: "user-123"}

	// allocation-a -> allocation-b -> allocation-a (all should be new exposures)
	allocations := []string{"allocation-a", "allocation-b", "allocation-a"}

	for i, allocation := range allocations {
		value := exposureCacheValue{allocationKey: allocation, variant: "variant-a"}
		result := cache.add(key, value)
		if !result {
			t.Errorf("allocation cycle step %d (%s) should return true", i+1, allocation)
		}
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	capacity := 3
	cache := newExposureLRUCache(capacity)
	value := exposureCacheValue{allocationKey: "alloc", variant: "variant"}

	// Fill cache to capacity: user-0, user-1, user-2
	// Order after: user-2 (front), user-1, user-0 (back/oldest)
	for i := 0; i < capacity; i++ {
		key := exposureCacheKey{flagKey: "flag", targetingKey: fmt.Sprintf("user-%d", i)}
		cache.add(key, value)
	}

	// Add one more to trigger eviction of oldest (user-0)
	// Order after: user-new (front), user-2, user-1 (back/oldest)
	cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-new"}, value)

	// Re-adding evicted entry should return true (it's new again)
	// This also triggers eviction of user-1 (now oldest)
	// Order after: user-0 (front), user-new, user-2 (back/oldest)
	result := cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-0"}, value)
	if !result {
		t.Error("re-adding evicted entry should return true")
	}

	// user-2 should still be in cache (wasn't evicted)
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-2"}, value)
	if result {
		t.Error("user-2 should still be cached, add should return false")
	}

	// user-1 was evicted when user-0 was re-added
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-1"}, value)
	if !result {
		t.Error("user-1 should have been evicted")
	}
}

func TestLRUCache_LRUOrdering(t *testing.T) {
	capacity := 3
	cache := newExposureLRUCache(capacity)
	value := exposureCacheValue{allocationKey: "alloc", variant: "variant"}

	// Add entries: user-0, user-1, user-2
	// LRU order (back=least recent): user-0, user-1, user-2
	cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-0"}, value)
	cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-1"}, value)
	cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-2"}, value)

	// Access user-0 again (moves it to most recently used)
	// LRU order: user-1 (least recent), user-2, user-0 (most recent)
	cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-0"}, value)

	// Add new entry - should evict user-1 (least recently used)
	// LRU order: user-2, user-0, user-new (most recent)
	cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-new"}, value)

	// user-0 should still be cached (was recently accessed)
	result := cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-0"}, value)
	if result {
		t.Error("user-0 should still be cached after recent access")
	}

	// user-2 should still be cached (check BEFORE user-1 to avoid eviction)
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-2"}, value)
	if result {
		t.Error("user-2 should still be cached")
	}

	// user-1 should have been evicted (was least recently used)
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-1"}, value)
	if !result {
		t.Error("user-1 should have been evicted")
	}
}

func TestLRUCache_EmptyStrings(t *testing.T) {
	cache := newExposureLRUCache(100)

	// Empty flag key
	result := cache.add(exposureCacheKey{flagKey: "", targetingKey: "user"}, exposureCacheValue{allocationKey: "alloc", variant: "variant"})
	if !result {
		t.Error("empty flag key should still be added")
	}

	// Empty subject
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: ""}, exposureCacheValue{allocationKey: "alloc", variant: "variant"})
	if !result {
		t.Error("empty subject should still be added")
	}

	// All empty
	result = cache.add(exposureCacheKey{flagKey: "", targetingKey: ""}, exposureCacheValue{allocationKey: "", variant: ""})
	if !result {
		t.Error("all empty strings should still be added")
	}

	// Duplicate of all empty should return false
	result = cache.add(exposureCacheKey{flagKey: "", targetingKey: ""}, exposureCacheValue{allocationKey: "", variant: ""})
	if result {
		t.Error("duplicate empty entry should return false")
	}
}

func TestLRUCache_ZeroCapacity(t *testing.T) {
	cache := newExposureLRUCache(0)

	key := exposureCacheKey{flagKey: "flag", targetingKey: "user"}
	value := exposureCacheValue{allocationKey: "alloc", variant: "variant"}
	// With zero capacity, nothing is cached - every add is "new"
	for i := 0; i < 5; i++ {
		result := cache.add(key, value)
		if !result {
			t.Errorf("zero capacity cache: add #%d should return true (no caching)", i+1)
		}
	}
}

func TestLRUCache_SingleCapacity(t *testing.T) {
	cache := newExposureLRUCache(1)
	value := exposureCacheValue{allocationKey: "alloc", variant: "variant"}

	// First add
	result := cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-1"}, value)
	if !result {
		t.Error("first add should return true")
	}

	// Duplicate should return false
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-1"}, value)
	if result {
		t.Error("duplicate should return false")
	}

	// New entry evicts the old one
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-2"}, value)
	if !result {
		t.Error("new entry should return true")
	}

	// Old entry was evicted, re-adding returns true
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-1"}, value)
	if !result {
		t.Error("evicted entry re-added should return true")
	}

	// user-2 was evicted when user-1 was re-added
	result = cache.add(exposureCacheKey{flagKey: "flag", targetingKey: "user-2"}, value)
	if !result {
		t.Error("user-2 should have been evicted")
	}
}

func TestExposureWriter_ConcurrentAppend(t *testing.T) {
	// Create a writer with a mock/nil HTTP client (we only care about the cache behavior)
	writer := &exposureWriter{
		buffer: make([]exposureEvent, 0, 256),
		cache:  newExposureLRUCache(1000),
	}

	const numGoroutines = 100
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Concurrent appends simulating multiple goroutines evaluating flags
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				event := exposureEvent{
					Timestamp:  int64(i),
					Allocation: exposureAllocation{Key: fmt.Sprintf("alloc-%d", goroutineID%3)},
					Flag:       exposureFlag{Key: fmt.Sprintf("flag-%d", i%10)},
					Variant:    exposureVariant{Key: fmt.Sprintf("variant-%d", i%2)},
					Subject:    exposureSubject{ID: fmt.Sprintf("user-%d", i%10)},
				}
				writer.append(event)
			}
		}(g)
	}

	wg.Wait()

	// Verify no panic occurred and buffer has events
	// Due to deduplication, we expect fewer events than total operations
	if len(writer.buffer) == 0 {
		t.Error("expected some events in buffer after concurrent appends")
	}

	// With 10 unique (flag, subject) pairs and varying allocations/variants,
	// we should have significantly fewer events than numGoroutines * opsPerGoroutine
	totalOps := numGoroutines * opsPerGoroutine
	if len(writer.buffer) >= totalOps {
		t.Errorf("deduplication not working: got %d events, expected fewer than %d", len(writer.buffer), totalOps)
	}
}

func TestExposureWriter_ConcurrentAppend_Deduplication(t *testing.T) {
	writer := &exposureWriter{
		buffer: make([]exposureEvent, 0, 256),
		cache:  newExposureLRUCache(1000),
	}

	const numGoroutines = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// All goroutines append the exact same event - only 1 should make it through
	for g := 0; g < numGoroutines; g++ {
		go func() {
			defer wg.Done()
			event := exposureEvent{
				Timestamp:  12345,
				Allocation: exposureAllocation{Key: "same-alloc"},
				Flag:       exposureFlag{Key: "same-flag"},
				Variant:    exposureVariant{Key: "same-variant"},
				Subject:    exposureSubject{ID: "same-user"},
			}
			writer.append(event)
		}()
	}

	wg.Wait()

	// Only 1 event should be in the buffer due to deduplication
	if len(writer.buffer) != 1 {
		t.Errorf("expected exactly 1 event due to deduplication, got %d", len(writer.buffer))
	}
}

func TestExposureHook_After(t *testing.T) {
	tests := []struct {
		name              string
		targetingKey      string
		attributes        map[string]interface{}
		flagKey           string
		variant           string
		metadata          of.FlagMetadata
		expectEvent       bool
		expectedSubjectID string
	}{
		{
			name:         "with targeting key",
			targetingKey: "user-123",
			attributes:   map[string]interface{}{"org_id": 456},
			flagKey:      "test-flag",
			variant:      "variant-a",
			metadata: of.FlagMetadata{
				metadataAllocationKey: "test-allocation",
				metadataDoLogKey:      true,
			},
			expectEvent:       true,
			expectedSubjectID: "user-123",
		},
		{
			name:         "with empty targeting key",
			targetingKey: "",
			attributes:   map[string]interface{}{"org_id": 789},
			flagKey:      "server-side-flag",
			variant:      "enabled",
			metadata: of.FlagMetadata{
				metadataAllocationKey: "server-allocation",
				metadataDoLogKey:      true,
			},
			expectEvent:       true,
			expectedSubjectID: "",
		},
		{
			name:         "missing allocation key",
			targetingKey: "user-123",
			attributes:   map[string]interface{}{},
			flagKey:      "test-flag",
			variant:      "variant-a",
			metadata:     of.FlagMetadata{}, // no allocation key
			expectEvent:  false,
		},
		{
			name:         "doLog is false",
			targetingKey: "user-123",
			attributes:   map[string]interface{}{},
			flagKey:      "test-flag",
			variant:      "variant-a",
			metadata: of.FlagMetadata{
				metadataAllocationKey: "test-allocation",
				metadataDoLogKey:      false,
			},
			expectEvent: false,
		},
		{
			name:         "nil metadata",
			targetingKey: "user-123",
			attributes:   map[string]interface{}{},
			flagKey:      "test-flag",
			variant:      "variant-a",
			metadata:     nil,
			expectEvent:  false, // no allocation key means no event
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a writer to capture events
			writer := &exposureWriter{
				buffer: make([]exposureEvent, 0, 256),
				cache:  newExposureLRUCache(1000),
			}
			hook := newExposureHook(writer)

			// Create evaluation context
			evalCtx := of.NewEvaluationContext(tc.targetingKey, tc.attributes)

			// Create hook context
			hookCtx := of.NewHookContext(
				tc.flagKey,
				of.Boolean,
				false,
				of.ClientMetadata{},
				of.Metadata{},
				evalCtx,
			)

			// Create evaluation details
			details := of.InterfaceEvaluationDetails{
				Value: true,
				EvaluationDetails: of.EvaluationDetails{
					FlagKey:  tc.flagKey,
					FlagType: of.Boolean,
					ResolutionDetail: of.ResolutionDetail{
						Variant:      tc.variant,
						FlagMetadata: tc.metadata,
					},
				},
			}

			// Call After hook
			err := hook.After(context.Background(), hookCtx, details, of.HookHints{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check if event was created
			if tc.expectEvent {
				if len(writer.buffer) != 1 {
					t.Errorf("expected 1 event, got %d", len(writer.buffer))
					return
				}
				event := writer.buffer[0]
				if event.Subject.ID != tc.expectedSubjectID {
					t.Errorf("expected subject ID %q, got %q", tc.expectedSubjectID, event.Subject.ID)
				}
				if event.Flag.Key != tc.flagKey {
					t.Errorf("expected flag key %q, got %q", tc.flagKey, event.Flag.Key)
				}
				if event.Variant.Key != tc.variant {
					t.Errorf("expected variant %q, got %q", tc.variant, event.Variant.Key)
				}
			} else {
				if len(writer.buffer) != 0 {
					t.Errorf("expected no events, got %d", len(writer.buffer))
				}
			}
		})
	}
}

func TestExposureHook_After_EmptyTargetingKeyWithAttributes(t *testing.T) {
	// This test specifically verifies that exposures with empty targeting keys
	// but with attributes are correctly logged (server-side evaluations)
	writer := &exposureWriter{
		buffer: make([]exposureEvent, 0, 256),
		cache:  newExposureLRUCache(1000),
	}
	hook := newExposureHook(writer)

	// Server-side evaluation: no user, but has org context
	evalCtx := of.NewEvaluationContext("", map[string]interface{}{
		"org_id":  12345,
		"service": "backend-service",
	})

	hookCtx := of.NewHookContext(
		"feature-rollout",
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)

	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "feature-rollout",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant: "enabled",
				FlagMetadata: of.FlagMetadata{
					metadataAllocationKey: "org-rollout",
					metadataDoLogKey:      true,
				},
			},
		},
	}

	err := hook.After(context.Background(), hookCtx, details, of.HookHints{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(writer.buffer) != 1 {
		t.Fatalf("expected 1 event, got %d", len(writer.buffer))
	}

	event := writer.buffer[0]

	// Subject ID should be empty
	if event.Subject.ID != "" {
		t.Errorf("expected empty subject ID, got %q", event.Subject.ID)
	}

	// Attributes should be populated
	if event.Subject.Attributes == nil {
		t.Error("expected subject attributes to be populated")
	}

	// Check that org_id is in attributes
	if orgID, ok := event.Subject.Attributes["org_id"]; !ok {
		t.Error("expected org_id in subject attributes")
	} else if orgID != int64(12345) && orgID != 12345 {
		t.Errorf("expected org_id to be 12345, got %v (type %T)", orgID, orgID)
	}
}

func TestExposureHook_After_ContextCancelled(t *testing.T) {
	writer := &exposureWriter{
		buffer: make([]exposureEvent, 0, 256),
		cache:  newExposureLRUCache(1000),
	}
	hook := newExposureHook(writer)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evalCtx := of.NewEvaluationContext("user-123", nil)
	hookCtx := of.NewHookContext(
		"test-flag",
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)

	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "test-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant: "enabled",
				FlagMetadata: of.FlagMetadata{
					metadataAllocationKey: "test-allocation",
				},
			},
		},
	}

	err := hook.After(ctx, hookCtx, details, of.HookHints{})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", err)
	}

	// No event should be logged
	if len(writer.buffer) != 0 {
		t.Errorf("expected no events when context cancelled, got %d", len(writer.buffer))
	}
}
