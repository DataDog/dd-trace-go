// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package slog provides functions to correlate logs and traces using log/slog package (https://pkg.go.dev/log/slog).
package slog // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/log/slog"

import (
	"io"
	"log/slog"

	v2 "github.com/DataDog/dd-trace-go/contrib/log/slog/v2"
)

// NewJSONHandler is a convenience function that returns a *slog.JSONHandler logger enhanced with
// tracing information.
func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	return v2.NewJSONHandler(w, opts)
}

// WrapHandler enhances the given logger handler attaching tracing information to logs.
func WrapHandler(h slog.Handler) slog.Handler {
	return v2.WrapHandler(h)
}
