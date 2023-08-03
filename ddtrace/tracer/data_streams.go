// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/dsminterface"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

func SetDataStreamsCheckpoint(ctx context.Context, edgeTags ...string) (dsminterface.Pathway, context.Context) {
	if p := internal.GetGlobalTracer().DataStreamsProcessor(); p != nil {
		return p.SetCheckpoint(ctx, edgeTags...)
	}
	return nil, ctx
}

func TrackKafkaCommitOffset(group, topic string, partition int32, offset int64) {
	if p := internal.GetGlobalTracer().DataStreamsProcessor(); p != nil {
		p.TrackKafkaCommitOffset(group, topic, partition, offset)
	}
}

func TrackKafkaProduceOffset(topic string, partition int32, offset int64) {
	if p := internal.GetGlobalTracer().DataStreamsProcessor(); p != nil {
		p.TrackKafkaProduceOffset(topic, partition, offset)
	}
}
