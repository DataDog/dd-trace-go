// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

// spansPerRequest is the HTTP client and server spans, plus the
// graphql.request and graphql.field spans.
const spansPerRequest = 4

// TestCaseParseSchema checks that a schema from graphql.ParseSchema is traced
// once.
type TestCaseParseSchema struct {
	TestCase
}

func (*TestCaseParseSchema) ExpectedSpanCount() int {
	return spansPerRequest
}
