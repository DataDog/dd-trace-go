// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpanMetaReserve(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial map[string]string
		n       int
		wantNil bool
	}{
		{name: "nil-small", n: 8, wantNil: true},
		{name: "nil-zero", n: 0, wantNil: true},
		{name: "nil-large", n: 9},
		{name: "existing", initial: map[string]string{"k": "v"}, n: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := SpanMeta{m: tc.initial}
			sm.Reserve(tc.n)
			if tc.wantNil {
				assert.Nil(t, sm.m)
				return
			}
			assert.NotNil(t, sm.m)
			if tc.initial != nil {
				assert.Equal(t, map[string]string{"k": "v"}, sm.m)
				sm.m["x"] = "y"
				assert.Equal(t, "y", tc.initial["x"], "existing map must be kept, not replaced")
			}
		})
	}
}
