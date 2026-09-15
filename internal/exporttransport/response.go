// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package exporttransport

import (
	"strings"
	"unicode/utf8"
)

const responseSnippetMaxBytes = 512

// ResponseSnippet returns a bounded, valid UTF-8 server diagnostic.
func ResponseSnippet(body []byte) string {
	snippet := strings.ToValidUTF8(strings.TrimSpace(string(body)), "")
	if len(snippet) <= responseSnippetMaxBytes {
		return snippet
	}
	cut := responseSnippetMaxBytes
	for cut > 0 && !utf8.RuneStart(snippet[cut]) {
		cut--
	}
	return snippet[:cut]
}
