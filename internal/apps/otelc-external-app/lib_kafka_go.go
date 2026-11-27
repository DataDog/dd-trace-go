// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"

	"github.com/segmentio/kafka-go"
)

func init() {
	registerLibrary("kafka_go", func() {
		// Writing no messages returns before any network call.
		_ = (&kafka.Writer{Addr: kafka.TCP("localhost:9092")}).WriteMessages(context.Background())
	})
}
