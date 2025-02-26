// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"github.com/DataDog/dd-trace-go/tools/v2check/v2check"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	c := v2check.NewChecker(
		&v2check.V1ImportURL{},
		&v2check.DDTraceTypes{},
		&v2check.TracerStructs{},
		&v2check.TraceIDString{},
		&v2check.WithServiceName{},
	)
	c.Run(singlechecker.Main)
}
