// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"github.com/DataDog/dd-trace-go/tools/v2fix/v2fix"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	c := v2fix.NewChecker(
		&v2fix.V1ImportURL{},
		&v2fix.DDTraceTypes{},
		&v2fix.TracerStructs{},
		&v2fix.TraceIDString{},
		&v2fix.WithServiceName{},
		&v2fix.WithDogstatsdAddr{},
		&v2fix.DeprecatedSamplingRules{},
	)
	c.Run(singlechecker.Main)
}
