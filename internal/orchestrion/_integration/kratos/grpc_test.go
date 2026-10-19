// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/harness"
)

func TestGRPC(t *testing.T) { harness.Run(t, new(GRPCTestCase)) }

func TestStream(t *testing.T) { harness.Run(t, new(StreamCase)) }

func TestRejectedHTTP(t *testing.T) { harness.Run(t, &TestCase{reject: true}) }

func TestRejectedGRPC(t *testing.T) { harness.Run(t, &GRPCTestCase{reject: true}) }

func TestFinalHTTPStatus(t *testing.T) {
	for _, otel := range []bool{false, true} {
		for _, code := range []int{201, 204} {
			t.Run(strconv.Itoa(code)+"/otel="+strconv.FormatBool(otel), func(t *testing.T) {
				harness.Run(t, &TestCase{status: code, otel: otel})
			})
		}
	}
}
