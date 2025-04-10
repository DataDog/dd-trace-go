// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import "testing"

// BenchmarkSampleWAFContext benchmarks the creation of a WAF context and running the WAF on a request/response pair
// This is a basic sample of what could happen in a real-world scenario.
// Skipped because it depends on internal packages.
func BenchmarkSampleWAFContext(b *testing.B) {
	b.Skip()
}
