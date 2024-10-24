// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package mocktracer

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	// TODO: seelog (indirect dependency) has a known goroutine leak where it leaks a single goroutine on init (https://github.com/cihub/seelog/issues/182)
	goleak.VerifyTestMain(m, goleak.IgnoreAnyFunction("github.com/cihub/seelog.(*asyncLoopLogger).processQueue"))
}
