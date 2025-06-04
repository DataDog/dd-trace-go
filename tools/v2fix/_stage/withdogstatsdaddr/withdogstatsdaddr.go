// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	tracer.Start(tracer.WithDogstatsdAddress("localhost:8125")) // want `the function WithDogstatsdAddress is no longer supported. Use WithDogstatsdAddr instead.`
	tracer.Stop()
}
