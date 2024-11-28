// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package mgo_test

import (
	"log"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	ddmgo "github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2"
)

func Example() {
	// Ensure your tracer is started and stopped
	tracer.Start()
	defer tracer.Stop()

	// Start a new session
	session, err := ddmgo.Dial("localhost:8080", ddmgo.WithService("serviceName"))
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	// Trace the session
	result := struct{}{}
	session.Run("ping", &result)
}
