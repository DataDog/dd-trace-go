// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cloudevents_test

import (
	"log"

	cloudeventstrace "github.com/DataDog/dd-trace-go/contrib/cloudevents/sdk-go.v2/v2"
	"github.com/cloudevents/sdk-go/v2/client"
	"github.com/cloudevents/sdk-go/v2/protocol/gochan"
)

func ExampleNew() {
	observability := cloudeventstrace.New(
		cloudeventstrace.WithMessagingSystem("kafka"),
		cloudeventstrace.WithDestinationName("orders"),
	)
	c, err := client.New(gochan.New(), client.WithObservabilityService(observability))
	if err != nil {
		log.Fatal(err)
	}
	_ = c
}
