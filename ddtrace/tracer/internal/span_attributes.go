// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

// AttrKey is an integer index into a SpanAttributes value array.
// Use the pre-declared constants; do not construct AttrKey from arbitrary integers.
type AttrKey uint8

const (
	AttrEnv       AttrKey = 0
	AttrVersion   AttrKey = 1
	AttrComponent AttrKey = 2
	AttrSpanKind  AttrKey = 3
	numAttrs      AttrKey = 4
)

// Compile-time guard: the numeric values of AttrKey constants are load-bearing —
// they index both vals[] and setMask bit positions. If any value drifts (e.g. via
// iota + reorder), the expression below produces a compile error.
var (
	_ = [1]byte{}[AttrEnv]         // AttrEnv must be 0
	_ = [1]byte{}[AttrVersion-1]   // AttrVersion must be 1
	_ = [1]byte{}[AttrComponent-2] // AttrComponent must be 2
	_ = [1]byte{}[AttrSpanKind-3]  // AttrSpanKind must be 3
)

// SpanAttributes holds the four V1-protocol promoted span fields.
// Zero value = all fields absent.
// Set(key, "") is distinct from never-Set: the bit is set, the string is "".
//
// Layout: 1-byte setMask + 7B padding + [4]string (64B) = 72 bytes.
type SpanAttributes struct {
	setMask uint8
	vals    [numAttrs]string
}

func (a *SpanAttributes) Set(key AttrKey, v string)      { a.vals[key] = v; a.setMask |= 1 << key }
func (a *SpanAttributes) Val(key AttrKey) string         { return a.vals[key] }
func (a *SpanAttributes) Get(key AttrKey) (string, bool) { return a.vals[key], a.setMask>>key&1 != 0 }
