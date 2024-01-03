// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package options

import "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

// Copy should be used any time existing options are copied into
// a new locally scoped set of options. This is to avoid data races and
// accidental side effects.
func Copy(opts ...tracer.StartSpanOption) []tracer.StartSpanOption {
	dup := make([]tracer.StartSpanOption, len(opts))
	copy(dup, opts)
	return dup
}
