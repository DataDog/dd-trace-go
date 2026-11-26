// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package server builds a twirp server from a twirp.ServerOptions literal, as
// generated code does.
package server

import (
	"net/http"

	"github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/example"
)

// NewHaberdasherServer returns a server that uses the hooks of a
// twirp.ServerOptions literal built in this package.
func NewHaberdasherServer(svc example.Haberdasher) http.Handler {
	opts := &twirp.ServerOptions{}
	return example.NewHaberdasherServer(svc, twirp.WithServerHooks(opts.Hooks))
}
