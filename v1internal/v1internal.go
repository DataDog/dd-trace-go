// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package v1internal provides support for v1 as frontend of v2 implementation.
// Note that this package is for dd-trace-go internal usage only.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package v1internal

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/internal"
)

// SetPropagatingTag sets the key/value pair as a trace propagating tag.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func SetPropagatingTag(ctx ddtrace.SpanContext, k, v string) {
	internal.SetPropagatingTag(ctx, k, v)
}
