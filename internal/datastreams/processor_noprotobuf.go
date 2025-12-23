// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

//go:build noprotobuf

package datastreams

import (
	"context"
	"net/http"
	"net/url"

	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/internal"
)

type Processor struct{}

func NewProcessor(_ internal.StatsdClient, _, _, _ string, _ *url.URL, _ *http.Client) *Processor {
	return nil
}

func (*Processor) Start() {}

func (*Processor) Stop() {}

func (*Processor) Flush() {}

func (*Processor) SetCheckpointWithParams(ctx context.Context, _ options.CheckpointParams, _ ...string) context.Context {
	return ctx
}

func (*Processor) TrackKafkaCommitOffset(_, _ string, _ int32, _ int64) {
	panic("not implemented")
}

func (*Processor) TrackKafkaProduceOffset(_ string, _ int32, _ int64) {}

func (*Processor) TrackKafkaHighWatermarkOffset(_ string, _ string, _ int32, _ int64) {}
