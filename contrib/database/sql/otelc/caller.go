// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"runtime"
	"strings"
)

const contribPrefix = "github.com/DataDog/dd-trace-go/contrib/database/sql/v2."

// calledByContrib reports whether the first caller outside database/sql is the
// contrib. The contrib calls database/sql.Open and OpenDB to build what it
// returns, so hooking those calls would recurse.
func calledByContrib() bool {
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	inSQL := false
	for {
		f, more := frames.Next()
		switch {
		case strings.HasPrefix(f.Function, "database/sql."):
			inSQL = true
		case inSQL:
			return strings.HasPrefix(f.Function, contribPrefix)
		}
		if !more {
			return false
		}
	}
}
