// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package gearbox

import (
	"log"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gogearbox/gearbox"
)

func Example() {

	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	gb := gearbox.New()
	gb.Use(Datadog)

	err := gb.Start(":8080")
	if err != nil {
		log.Fatal(err)
	}

}
