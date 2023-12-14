// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package options

import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace"

// Copy should be used any time existing options are copied into
// a new locally scoped set of options. This is to avoid data races and
// accidental side effects.
func Copy(opts ...ddtrace.StartSpanOption) []ddtrace.StartSpanOption {
	dup := make([]ddtrace.StartSpanOption, len(opts))
	copy(dup, opts)
	return dup
}
