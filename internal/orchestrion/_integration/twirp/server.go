// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package twirp

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twitchtv/twirp/example"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// spansPerRequest is the net/http client span, the net/http server span and
// the twirp span of one call.
const spansPerRequest = 3

// serve starts handler on a local port and returns a client for it.
func serve(t *testing.T, handler http.Handler) example.Haberdasher {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{Handler: handler}
	go func() {
		assert.ErrorIs(t, srv.Serve(lis), http.ErrServerClosed)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, srv.Shutdown(ctx))
	})

	return example.NewHaberdasherJSONClient("http://"+lis.Addr().String(), http.DefaultClient)
}

// makeHat makes one call, which gives one trace.
func makeHat(ctx context.Context, t *testing.T, client example.Haberdasher) {
	_, err := client.MakeHat(ctx, &example.Size{Inches: 6})
	require.NoError(t, err)
}

// expectedTraces is the trace of one call. The cases below share it with
// TestCase.
func expectedTraces() trace.Traces {
	return new(TestCase).ExpectedTraces()
}
