// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package internal

import (
	"context"
	"math"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "kafka"

type Config struct {
	Ctx                 context.Context
	ConsumerServiceName string
	ProducerServiceName string
	ConsumerSpanName    string
	ProducerSpanName    string
	AnalyticsRate       float64
	BootstrapServers    string
	GroupID             string
	TagFns              map[string]func(msg *kafka.Message) interface{}
	DataStreamsEnabled  bool
}

type Option func(cfg *Config)

func NewConfig(opts ...Option) *Config {
	cfg := &Config{
		Ctx: context.Background(),
		// analyticsRate: globalconfig.AnalyticsRate(),
		AnalyticsRate: math.NaN(),
	}
	cfg.DataStreamsEnabled = internal.BoolEnv("DD_DATA_STREAMS_ENABLED", false)
	if internal.BoolEnv("DD_TRACE_KAFKA_ANALYTICS_ENABLED", false) {
		cfg.AnalyticsRate = 1.0
	}

	cfg.ConsumerServiceName = namingschema.ServiceName(defaultServiceName)
	cfg.ProducerServiceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.ConsumerSpanName = namingschema.OpName(namingschema.KafkaInbound)
	cfg.ProducerSpanName = namingschema.OpName(namingschema.KafkaOutbound)

	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}
