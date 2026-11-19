// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeTraceID(t *testing.T) {
	for _, tt := range []struct {
		name, in, want string
	}{
		{"hex with a-f is kept", "6928d1a700000000abcdef0123456789", "6928d1a700000000abcdef0123456789"},
		{"hex with a leading zero is kept", "0123456789012345678901234567890a", "0123456789012345678901234567890a"},
		{"all-digit hex with a leading zero is kept", "01234567890123456789012345678901", "01234567890123456789012345678901"},
		{"128-bit decimal becomes hex", "139780883686467862853683758018840586121", "6928d1a700000000abcdef0123456789"},
		{"64-bit decimal stays decimal", "18446744073709551615", "18446744073709551615"},
		{"just over 64 bits becomes padded hex", "18446744073709551616", "00000000000000010000000000000000"},
		{"32-char all-digit value is read as decimal", "12345678901234567890123456789012", "0000009bd30a3c645943dd1690a03a14"},
		{"over 128 bits is kept", "340282366920938463463374607431768211456", "340282366920938463463374607431768211456"},
		{"custom ID is kept", "my-trace", "my-trace"},
		{"uppercase hex is kept as-is", "6928D1A700000000ABCDEF0123456789", "6928D1A700000000ABCDEF0123456789"},
		{"empty", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeTraceID(tt.in))
		})
	}
}

func TestIsCanonicalTraceID(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"6928d1a700000000abcdef0123456789", true},
		{"00000000000000010000000000000000", true},
		{"12345", true},
		{"18446744073709551615", true},
		{"18446744073709551616", false},
		{"0123", false},
		{"0", false},
		{"-1", false},
		{"6928D1A700000000ABCDEF0123456789", false},
		{"6928d1a700000000abcdef01234567890", false},
		{"", false},
	} {
		assert.Equal(t, tt.want, IsCanonicalTraceID(tt.in), tt.in)
	}
}
