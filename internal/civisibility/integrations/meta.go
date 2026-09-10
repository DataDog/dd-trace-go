// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

// ciVisibilityMetaValueMaxChars is the maximum number of characters allowed in
// CI Visibility event meta values.
const ciVisibilityMetaValueMaxChars = 5000

// ciVisibilityTag returns a tracer tag option with CI Visibility meta string
// limits applied.
func ciVisibilityTag(key string, value any) tracer.StartSpanOption {
	return tracer.Tag(key, truncateCIVisibilityTagValue(value))
}

// setCIVisibilitySpanTag sets a span tag with CI Visibility meta string limits
// applied.
func setCIVisibilitySpanTag(span *tracer.Span, key string, value any) {
	span.SetTag(key, truncateCIVisibilityTagValue(value))
}

// truncateCIVisibilityTagValue limits string tag values while leaving metric
// and boolean tag values unchanged.
func truncateCIVisibilityTagValue(value any) any {
	if v, ok := value.(string); ok {
		return truncateCIVisibilityMetaValue(v)
	}
	return value
}

// truncateCIVisibilityMetaValue returns v limited to
// ciVisibilityMetaValueMaxChars characters while preserving UTF-8 boundaries.
func truncateCIVisibilityMetaValue(v string) string {
	if len(v) <= ciVisibilityMetaValueMaxChars {
		return v
	}
	count := 0
	for i := range v {
		if count == ciVisibilityMetaValueMaxChars {
			return v[:i]
		}
		count++
	}
	return v
}
