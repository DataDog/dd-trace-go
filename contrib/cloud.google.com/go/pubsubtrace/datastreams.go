// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsubtrace

import (
	"context"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const dataStreamsTypeTag = "type:google-pubsub"

type dataStreamsCarrier map[string]string

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = dataStreamsCarrier(nil)

var _ datastreams.TextMapReaderByKey = dataStreamsCarrier(nil)

func (c dataStreamsCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (c dataStreamsCarrier) Get(key string) (string, bool) {
	v, ok := c[key]
	return v, ok
}

func (c dataStreamsCarrier) Set(key, val string) {
	c[key] = val
}

func produceEdgeTags(topic Topic) []string {
	return []string{"direction:out", "topic:" + edgeResourceName(topic.String(), "topics"), dataStreamsTypeTag}
}

func consumeEdgeTags(s Subscription) []string {
	return []string{"direction:in", "subscription:" + edgeResourceName(s.String(), "subscriptions"), dataStreamsTypeTag}
}

func edgeResourceName(name, collection string) string {
	if isResourcePath(name, collection) {
		return strings.Split(name, "/")[3]
	}
	return name
}

func dataStreamsPayloadSize(msg *Message) int64 {
	size := int64(len(msg.Data))
	for k, v := range msg.Attributes {
		size += int64(len(k) + len(v))
	}
	return size
}

func (tr *Tracer) produceCheckpoint(ctx context.Context, topic Topic, msg *Message) {
	if msg == nil {
		return
	}
	checkpointCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
		ctx,
		options.CheckpointParams{PayloadSize: dataStreamsPayloadSize(msg)},
		produceEdgeTags(topic)...,
	)
	if ok {
		datastreams.InjectToBase64Carrier(checkpointCtx, dataStreamsCarrier(msg.Attributes))
	}
}

func (tr *Tracer) consumeCheckpoint(s Subscription, msg *Message) (context.Context, bool) {
	if msg == nil {
		return nil, false
	}
	parent := datastreams.ExtractFromBase64Carrier(context.Background(), dataStreamsCarrier(msg.Attributes))
	return tracer.SetDataStreamsCheckpointWithParams(
		parent,
		options.CheckpointParams{PayloadSize: dataStreamsPayloadSize(msg)},
		consumeEdgeTags(s)...,
	)
}
