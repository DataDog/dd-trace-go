// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

type CKGoVersion int32

const (
	CKGoVersion1 CKGoVersion = 1
	CKGoVersion2 CKGoVersion = 2
)

func componentName(v CKGoVersion) string {
	switch v {
	case CKGoVersion1:
		return "confluentinc/confluent-kafka-go/kafka"
	case CKGoVersion2:
		return "confluentinc/confluent-kafka-go/kafka.v2"
	default:
		return ""
	}
}

func integrationName(v CKGoVersion) string {
	switch v {
	case CKGoVersion1:
		return "github.com/confluentinc/confluent-kafka-go"
	case CKGoVersion2:
		return "github.com/confluentinc/confluent-kafka-go/v2"
	default:
		return ""
	}
}

func InitInstrumentation(v CKGoVersion) {
	cn := componentName(v)
	in := integrationName(v)
	if cn == "" || in == "" {
		panic(fmt.Sprintf("invalid confluent-kafka-go version: %d", v))
	}
	telemetry.LoadIntegration(cn)
	tracer.MarkIntegrationImported(in)
}
