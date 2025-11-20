// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import "testing"

func BenchmarkParseSymbol(b *testing.B) {
	testCases := []string{
		"github.com/DataDog/dd-trace-go/v2/internal/stacktrace.TestFunc",
		"github.com/DataDog/dd-trace-go/v2/internal/stacktrace.(*Event).NewException",
		"github.com/DataDog/dd-trace-go/v2/internal/stacktrace.TestFunc.func1",
		"os/exec.(*Cmd).Run.func1",
		"test.(*Test).Method",
		"test.main",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, tc := range testCases {
			_ = parseSymbol(tc)
		}
	}
}
