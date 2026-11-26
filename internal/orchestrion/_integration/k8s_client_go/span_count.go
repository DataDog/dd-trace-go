// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package k8sclientgo

// spansPerRequest is the client-go span, the net/http client span and the
// net/http server span of one API request.
const spansPerRequest = 3

// The cases below run the existing ones again and check the exact span count,
// which the trace match alone does not, so a config wrapped twice fails.

type TestCaseNewCfgFuncSpanCount struct {
	TestCaseNewCfgFunc
}

func (*TestCaseNewCfgFuncSpanCount) ExpectedSpanCount() int {
	return spansPerRequest
}

type TestCaseStructLiteralWithParamSpanCount struct {
	TestCaseStructLiteralWithParam
}

func (*TestCaseStructLiteralWithParamSpanCount) ExpectedSpanCount() int {
	return spansPerRequest
}

type TestCaseStructLiteralWithoutParamSpanCount struct {
	TestCaseStructLiteralWithoutParam
}

func (*TestCaseStructLiteralWithoutParamSpanCount) ExpectedSpanCount() int {
	return spansPerRequest
}
