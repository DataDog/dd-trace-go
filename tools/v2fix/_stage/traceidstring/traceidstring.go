// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	tracer.Start(tracer.WithService("service"))
	defer tracer.Stop()

	sp := tracer.StartSpan("opname")
	fmt.Printf("traceID: %d\n", sp.Context().TraceID()) // want `trace IDs are now represented as strings, please use TraceIDLower to keep using 64-bits IDs, although it's recommended to switch to 128-bits with TraceID`
}
