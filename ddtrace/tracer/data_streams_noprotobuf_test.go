// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

//go:build noprotobuf

package tracer

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func TestTrackKafkaCommitOffset(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DSM panicked: %v", r)
		}
	}()
	Start(WithTestDefaults(nil), WithLogger(log.DiscardLogger{}))
	defer Stop()

	TrackKafkaCommitOffset("group", "topic", 1, 0)
}

func TestNewProcessorNoProtobuf(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")

	Start(WithTestDefaults(nil), WithLogger(log.DiscardLogger{}))
	defer Stop()

	var (
		tr dataStreamsContainer
		ok bool
	)
	if tr, ok = getGlobalTracer().(dataStreamsContainer); !ok {
		t.Fatalf("Tracer doesn't support DSM")
	}
	if p := tr.GetDataStreamsProcessor(); p != nil {
		t.Fatalf("NewProcessor with tag noprotobuf should return nil")
	}
}
