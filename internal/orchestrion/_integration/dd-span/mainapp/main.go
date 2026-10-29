// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp has //dd:span functions in package main, which the harness
// suites never build. TestSpansInMain builds and runs it.
package main

import (
	"context"
	"errors"
	"fmt"
)

func main() {
	run()

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

//dd:span app:main
func run() {
	work(context.Background())
	_ = fail()
}

//dd:span app:work
func work(ctx context.Context) {}

//dd:span app:fail span.name:main.fail
func fail() error {
	return errors.New("main error")
}
