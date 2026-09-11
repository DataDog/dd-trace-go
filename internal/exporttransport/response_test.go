// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package exporttransport

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestResponseSnippet(t *testing.T) {
	assert.Equal(t, "boom", ResponseSnippet([]byte("  boom \n")))
	assert.Empty(t, ResponseSnippet(nil))
	assert.Len(t, ResponseSnippet([]byte(strings.Repeat("a", responseSnippetMaxBytes+100))), responseSnippetMaxBytes)
	assert.True(t, utf8.ValidString(ResponseSnippet([]byte(strings.Repeat("é", responseSnippetMaxBytes)))))
	boundary := strings.Repeat("a", responseSnippetMaxBytes-1)
	assert.Equal(t, boundary, ResponseSnippet([]byte(boundary+"é")))
	assert.Equal(t, "ok", ResponseSnippet([]byte{'o', 'k', 0xff, 0xfe}))
}
