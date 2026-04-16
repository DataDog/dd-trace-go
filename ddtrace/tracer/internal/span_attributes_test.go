// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"maps"
	"testing"
)

func TestSpanAttributesZeroValue(t *testing.T) {
	var a SpanAttributes
	for _, key := range []AttrKey{AttrEnv, AttrVersion, AttrLanguage} {
		if v, ok := a.Get(key); ok || v != "" {
			t.Errorf("key %d: expected absent zero value, got (%q, %v)", key, v, ok)
		}
	}
}

func TestSpanAttributesSetAndGet(t *testing.T) {
	tests := []struct {
		key AttrKey
		val string
	}{
		{AttrEnv, "prod"},
		{AttrVersion, "1.2.3"},
		{AttrLanguage, "go"},
	}
	var a SpanAttributes
	for _, tt := range tests {
		a.Set(tt.key, tt.val)
	}
	for _, tt := range tests {
		got, ok := a.Get(tt.key)
		if !ok {
			t.Errorf("key %d: expected present, got absent", tt.key)
		}
		if got != tt.val {
			t.Errorf("key %d: expected %q, got %q", tt.key, tt.val, got)
		}
		if a.Val(tt.key) != tt.val {
			t.Errorf("key %d: Val returned %q, expected %q", tt.key, a.Val(tt.key), tt.val)
		}
	}
}

// Set(key, "") is distinct from never-Set: the bit should be set and value "".
func TestSpanAttributesSetEmptyString(t *testing.T) {
	var a SpanAttributes
	a.Set(AttrEnv, "")
	v, ok := a.Get(AttrEnv)
	if !ok {
		t.Error("expected key to be marked present after Set with empty string")
	}
	if v != "" {
		t.Errorf("expected empty string value, got %q", v)
	}
}

func TestSpanAttributesSetOverwrite(t *testing.T) {
	var a SpanAttributes
	a.Set(AttrEnv, "staging")
	a.Set(AttrEnv, "prod")
	v, ok := a.Get(AttrEnv)
	if !ok {
		t.Error("expected key to be present")
	}
	if v != "prod" {
		t.Errorf("expected overwritten value %q, got %q", "prod", v)
	}
}

func TestSpanAttributesIndependentKeys(t *testing.T) {
	var a SpanAttributes
	a.Set(AttrEnv, "prod")

	// Other keys must remain absent.
	for _, key := range []AttrKey{AttrVersion, AttrLanguage} {
		if _, ok := a.Get(key); ok {
			t.Errorf("key %d should be absent after setting only AttrEnv", key)
		}
	}
}

func TestSpanAttributesSetNilSafe(t *testing.T) {
	var a *SpanAttributes
	// Must not panic.
	a.Set(AttrEnv, "prod")
}

func TestSpanAttributesValUnset(t *testing.T) {
	var a SpanAttributes
	// Val on an unset key returns "" without panicking.
	if v := a.Val(AttrVersion); v != "" {
		t.Errorf("expected empty string from unset key, got %q", v)
	}
}

func TestSpanAttributesForEach(t *testing.T) {
	var a SpanAttributes
	a.Set(AttrEnv, "prod")
	a.Set(AttrVersion, "1.2.3")

	got := maps.Collect(a.all())
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	if got["env"] != "prod" {
		t.Errorf("expected env=prod, got %q", got["env"])
	}
	if got["version"] != "1.2.3" {
		t.Errorf("expected version=1.2.3, got %q", got["version"])
	}
}

func TestSpanAttributesForEachNil(t *testing.T) {
	var a *SpanAttributes
	called := false
	for range a.all() {
		called = true
	}
	if called {
		t.Error("All() should not call fn on nil receiver")
	}
}

func TestAttrKeyForTag(t *testing.T) {
	tests := []struct {
		tag string
		key AttrKey
		ok  bool
	}{
		{"env", AttrEnv, true},
		{"version", AttrVersion, true},
		{"language", AttrLanguage, true},
		{"component", AttrUnknown, false},
		{"span.kind", AttrUnknown, false},
		{"unknown", AttrUnknown, false},
		{"", AttrUnknown, false},
	}
	for _, tt := range tests {
		key, ok := AttrKeyForTag(tt.tag)
		if ok != tt.ok || key != tt.key {
			t.Errorf("AttrKeyForTag(%q) = (%d, %v), want (%d, %v)", tt.tag, key, ok, tt.key, tt.ok)
		}
	}
}

// BenchmarkSpanAttributesSet benchmarks setting all four promoted fields using
// SpanAttributes versus an equivalent map[string]string.
func BenchmarkSpanAttributesSet(b *testing.B) {
	b.Run("SpanAttributes", func(b *testing.B) {
		a := SpanAttributes{}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			a.Set(AttrEnv, "prod")
			a.Set(AttrVersion, "1.2.3")
			a.Set(AttrLanguage, "go")
		}
		_ = a
	})

	b.Run("map", func(b *testing.B) {
		m := make(map[string]string, 3)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m["env"] = "prod"
			m["version"] = "1.2.3"
			m["language"] = "go"
		}
		_ = m
	})
}

// BenchmarkSpanAttributesGet benchmarks reading all promoted fields.
func BenchmarkSpanAttributesGet(b *testing.B) {
	b.Run("SpanAttributes", func(b *testing.B) {
		var a SpanAttributes
		a.Set(AttrEnv, "prod")
		a.Set(AttrVersion, "1.2.3")
		a.Set(AttrLanguage, "go")
		b.ReportAllocs()
		b.ResetTimer()
		var s string
		var ok bool
		for i := 0; i < b.N; i++ {
			s, ok = a.Get(AttrEnv)
			s, ok = a.Get(AttrVersion)
			s, ok = a.Get(AttrLanguage)
		}
		_, _ = s, ok
	})

	b.Run("map", func(b *testing.B) {
		m := map[string]string{
			"env":      "prod",
			"version":  "1.2.3",
			"language": "go",
		}
		b.ReportAllocs()
		b.ResetTimer()
		var s string
		var ok bool
		for i := 0; i < b.N; i++ {
			s, ok = m["env"]
			s, ok = m["version"]
			s, ok = m["language"]
		}
		_, _ = s, ok
	})
}

// TestSpanMetaSetPromotedEmptyString verifies that Set("env", "") on a span with
// no prior env records the key as present (presence bit set), rather than
// silently no-oping because Val() returns "" for unset keys.
func TestSpanMetaSetPromotedEmptyString(t *testing.T) {
	sm := NewSpanMeta(nil)
	sm.Set("env", "")
	v, ok := sm.Get("env")
	if !ok {
		t.Fatal("expected env to be present after Set(\"env\", \"\"), got absent")
	}
	if v != "" {
		t.Fatalf("expected empty string, got %q", v)
	}
}

// TestSpanMetaSetPromotedNoOpWhenPresent verifies that Set("env", value) when
// env is already set to the same value leaves the value unchanged, and that
// updating to a different value is observed correctly.
func TestSpanMetaSetPromotedNoOpWhenPresent(t *testing.T) {
	var a SpanAttributes
	a.Set(AttrEnv, "prod")
	a.MarkReadOnly()
	sm := NewSpanMeta(&a)

	// Same value: result must still be ("prod", true).
	sm.Set("env", "prod")
	v, ok := sm.Get("env")
	if !ok || v != "prod" {
		t.Fatalf("no-op case: expected (prod, true), got (%q, %v)", v, ok)
	}

	// Different value: must be updated.
	sm.Set("env", "staging")
	v, ok = sm.Get("env")
	if !ok || v != "staging" {
		t.Fatalf("update case: expected (staging, true), got (%q, %v)", v, ok)
	}
}

// BenchmarkMap measures the allocation cost of Map() with both tag store
// entries and promoted attrs set.
func BenchmarkMap(b *testing.B) {
	var a SpanAttributes
	a.Set(AttrEnv, "prod")
	a.Set(AttrVersion, "1.2.3")
	a.Set(AttrLanguage, "go")
	sm := NewSpanMeta(&a)
	sm.Set("key0", "value0")
	sm.Set("key1", "value1")
	sm.Set("key2", "value2")
	sm.Set("key3", "value3")
	sm.Set("key4", "value4")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = sm.Map(true)
	}
}

func BenchmarkAttrKeyForTag(b *testing.B) {
	tags := []string{"env", "version", "language", "component", "span.kind", "unknown"}
	b.ReportAllocs()
	b.ResetTimer()
	var k AttrKey
	var ok bool
	for i := 0; i < b.N; i++ {
		for _, tag := range tags {
			k, ok = AttrKeyForTag(tag)
		}
	}
	_, _ = k, ok
}

func BenchmarkSpanAttributesAll(b *testing.B) {
	var a SpanAttributes
	a.Set(AttrEnv, "prod")
	a.Set(AttrVersion, "1.2.3")
	a.Set(AttrLanguage, "go")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for k, v := range a.all() {
			_ = k
			_ = v
		}
	}
}
