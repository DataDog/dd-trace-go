// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"os"

	_ "github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
	_ "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func main() {
	defer log.Flush()
	switch os.Args[1] {
	case "disabled", "invalid-enabled-logs-once":
		fmt.Printf("enabled=%t\n", stacktrace.Enabled())
	case "depth":
		fmt.Printf("enabled=%t depth=%d\n", stacktrace.Enabled(), len(stacktrace.CaptureRaw(0).PCs))
	}
}
