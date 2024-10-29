// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"math"
)

type Tracer struct {
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	dataStreamsEnabled  bool
	kafkaCfg            KafkaConfig
}

func NewTracer(kafkaCfg KafkaConfig, opts ...Option) *Tracer {
	tr := &Tracer{
		// analyticsRate: globalConfig.AnalyticsRate(),
		analyticsRate: math.NaN(),
		kafkaCfg:      kafkaCfg,
	}
	kafkaCfg.cfg = newConfig(opts...)
	return tr
}
