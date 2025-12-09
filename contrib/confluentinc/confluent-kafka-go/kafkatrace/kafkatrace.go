// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafkatrace provides common tracing functionality for different confluentinc/confluent-kafka-go versions.
//
// This package is not meant to be used directly (use instead
// github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2 or
// github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2)
package kafkatrace

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

type CKGoVersion int32

const (
	CKGoVersion1 CKGoVersion = 1
	CKGoVersion2 CKGoVersion = 2
)

func ComponentName(v CKGoVersion) string {
	switch v {
	case CKGoVersion1:
		return "confluentinc/confluent-kafka-go/kafka"
	case CKGoVersion2:
		return "confluentinc/confluent-kafka-go/kafka.v2"
	default:
		return ""
	}
}

func Package(v CKGoVersion) *instrumentation.Instrumentation {
	switch v {
	case CKGoVersion2:
		return instrumentation.Load(instrumentation.PackageConfluentKafkaGoV2)
	default:
		return instrumentation.Load(instrumentation.PackageConfluentKafkaGo)
	}
}
