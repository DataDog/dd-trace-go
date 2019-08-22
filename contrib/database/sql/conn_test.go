// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package sql

import (
	"context"
	"testing"
	"time"

	"github.com/Code-Hex/dd-trace-go/ddtrace/ext"
	"github.com/Code-Hex/dd-trace-go/ddtrace/mocktracer"
	"github.com/google/go-cmp/cmp"
)

func TestWithSpanTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// set tags
	ctxTags := map[string]string{
		"ctx_tag1": "value1",
		"ctx_tag2": "value2",
		"ctx_tag3": "value3",
	}
	ctx := WithSpanTags(context.Background(), ctxTags)
	query := "SELECT 1"
	startTime := time.Date(2019, 1, 1, 1, 0, 0, 0, time.UTC)

	tp := &traceParams{
		cfg: &config{
			serviceName: "mysql",
		},
		driverName: "mysql",
	}

	// try trace
	tp.tryTrace(ctx, ext.SQLType, query, startTime, nil)

	// check span
	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("unexpected span size: %d", len(spans))
	}
	tags := spans[0].Tags()
	if len(tags) == 0 {
		t.Fatalf("unexpected tags size: %d", len(tags))
	}

	want := map[string]interface{}{
		"resource.name": "SELECT 1",
		"ctx_tag1":      "value1",
		"ctx_tag2":      "value2",
		"ctx_tag3":      "value3",
		"service.name":  "mysql",
		"span.type":     "sql",
		"_dd1.sr.eausr": 0.000000, // for analysis
	}
	if diff := cmp.Diff(want, tags); diff != "" {
		t.Errorf("failed (-want, +got):\n%v", diff)
	}
}
