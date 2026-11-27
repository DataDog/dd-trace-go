// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp has a //dd:span on func main. TestAnnotatedMain builds it
// with orchestrion, which starts and stops the tracer around main.
package main

//dd:span span.name:ddspan.main
func main() {
	child()
}

//dd:span span.name:ddspan.main.child
func child() {}
