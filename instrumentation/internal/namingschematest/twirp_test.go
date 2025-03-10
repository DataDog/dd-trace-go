// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"net"
	"net/http"
	"testing"

	twitchtvtrace "github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/example"
)

var twitchTVTwirp = harness.TestCase{
	Name: instrumentation.PackageTwitchTVTwirp,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []twitchtvtrace.Option
		if serviceOverride != "" {
			opts = append(opts, twitchtvtrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client, cleanup := startIntegrationTestServer(t, opts...)
		defer cleanup()
		_, err := client.MakeHat(context.Background(), &example.Size{Inches: 6})
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 3)
		assert.Equal(t, "twirp.Haberdasher", spans[0].OperationName())
		assert.Equal(t, "twirp.handler", spans[1].OperationName())
		assert.Equal(t, "twirp.request", spans[2].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 3)
		assert.Equal(t, "twirp.server.request", spans[0].OperationName())
		assert.Equal(t, "twirp.handler", spans[1].OperationName())
		assert.Equal(t, "twirp.client.request", spans[2].OperationName())
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"twirp-server", "twirp-server", "twirp-client"},
		DDService:       harness.RepeatString(harness.TestDDService, 3),
		ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 3),
	},
}

// Copied from contrib/twitchtv/twirp/twirp_test.go
type notifyListener struct {
	net.Listener
	ch chan<- struct{}
}

func (n *notifyListener) Accept() (c net.Conn, err error) {
	if n.ch != nil {
		close(n.ch)
		n.ch = nil
	}
	return n.Listener.Accept()
}

type haberdasher int32

func (h haberdasher) MakeHat(_ context.Context, size *example.Size) (*example.Hat, error) {
	if size.Inches != int32(h) {
		return nil, twirp.InvalidArgumentError("Inches", "Only size of %d is allowed")
	}
	hat := &example.Hat{
		Size:  size.Inches,
		Color: "purple",
		Name:  "doggie beanie",
	}
	return hat, nil
}

func startIntegrationTestServer(t *testing.T, opts ...twitchtvtrace.Option) (example.Haberdasher, func()) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)

	readyCh := make(chan struct{})
	nl := &notifyListener{Listener: l, ch: readyCh}

	hooks := twitchtvtrace.NewServerHooks(opts...)
	server := twitchtvtrace.WrapServer(example.NewHaberdasherServer(haberdasher(6), hooks), opts...)
	errCh := make(chan error)
	go func() {
		err := http.Serve(nl, server)
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case <-readyCh:
		break
	case err := <-errCh:
		l.Close()
		assert.FailNow(t, "server not started", err)
	}
	client := example.NewHaberdasherJSONClient("http://"+nl.Addr().String(), twitchtvtrace.WrapClient(http.DefaultClient, opts...))
	return client, func() {
		assert.NoError(t, l.Close())
	}
}
