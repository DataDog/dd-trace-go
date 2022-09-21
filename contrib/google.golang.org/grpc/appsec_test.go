// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	rig, err := newRig(false)
	require.NoError(t, err)
	defer rig.Close()

	client := rig.client

	t.Run("unary", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log"))
		res, err := client.Ping(ctx, &FixtureRequest{Name: "<script>alert('xss');</script>"})
		// Check that the handler was properly called
		require.NoError(t, err)
		require.Equal(t, "passed", res.Message)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// The request should have the attack attempts
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-941-100")) // XSS attack attempt
		require.True(t, strings.Contains(event, "ua0-600-55x")) // canary rule attack attempt
	})

	t.Run("stream", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)

		// Send a XSS attack
		err = stream.Send(&FixtureRequest{Name: "<script>alert('xss');</script>"})
		require.NoError(t, err)

		// Check that the handler was properly called
		res, err := stream.Recv()
		require.Equal(t, "passed", res.Message)
		require.NoError(t, err)

		// Send a SQLi attack
		err = stream.Send(&FixtureRequest{Name: "something UNION SELECT * from users"})
		require.NoError(t, err)

		// Check that the handler was properly called
		res, err = stream.Recv()
		require.Equal(t, "passed", res.Message)
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		// to flush the spans
		stream.Recv()

		finished := mt.FinishedSpans()
		require.Len(t, finished, 6)

		// The request should have the attack attempts
		event, _ := finished[5].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-941-100")) // XSS attack attempt
		require.True(t, strings.Contains(event, "crs-942-100")) // SQL-injection attack attempt
		require.True(t, strings.Contains(event, "ua0-600-55x")) // canary rule attack attempt
	})
}
