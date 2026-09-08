// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Command victim is a real non-test main used to prove the crashtracker
// orchestrion aspect injects crashtracker.Start() as the first statement of
// main. It does not import crashtracker or call Start: a crash report is
// produced only if orchestrion injected the call.
package main

func main() {
	panic("orchestrion injection victim crash")
}
