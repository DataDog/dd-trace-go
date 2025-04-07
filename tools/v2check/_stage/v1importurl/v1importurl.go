// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"    // want `import URL needs to be updated`
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer" // want `import URL needs to be updated`
)

func main() {
	tracer.Start()
	defer tracer.Stop()

	span := tracer.StartSpan("operation", tracer.Tag(ext.SpanType, "test"))
	defer span.Finish()
}
