// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package twirp

// TestCaseSpanCount runs TestCase again and checks the exact span count, which
// the trace match alone does not, so a server traced twice fails.
type TestCaseSpanCount struct {
	TestCase
}

func (*TestCaseSpanCount) ExpectedSpanCount() int {
	return spansPerRequest
}
