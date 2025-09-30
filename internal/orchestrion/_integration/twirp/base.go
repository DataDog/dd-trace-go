// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package twirp

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/example"
)

func Setup(t *testing.T) (string, example.Haberdasher) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := "http://" + lis.Addr().String()
	handler := example.NewHaberdasherServer(&randomHaberdasher{})
	srv := &http.Server{Handler: handler}

	go func() {
		assert.ErrorIs(t, srv.Serve(lis), http.ErrServerClosed)
	}()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, srv.Shutdown(ctx))
	})

	client := example.NewHaberdasherJSONClient(addr, http.DefaultClient)

	return addr, client
}

type randomHaberdasher struct{}

func (*randomHaberdasher) MakeHat(_ context.Context, size *example.Size) (*example.Hat, error) {
	if size.Inches <= 0 {
		return nil, twirp.InvalidArgumentError("Inches", "I can't make a hat that small!")
	}
	return &example.Hat{
		Size:  size.Inches,
		Color: "blue",
		Name:  "top hat",
	}, nil
}
