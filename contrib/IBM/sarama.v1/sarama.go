// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package sarama provides functions to trace the IBM/sarama package (https://github.com/IBM/sarama).
package sarama // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/IBM/sarama"

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "IBM/sarama"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/IBM/sarama")
}
