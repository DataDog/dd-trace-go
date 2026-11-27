// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package slogdep creates slog loggers inside a dependency module.
package slogdep

import (
	"context"
	"io"
	"log/slog"
)

// NewLogger returns a logger that writes JSON records to w.
func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, nil))
}

// Passthrough returns a logger whose handler forwards every call to the
// handler of l, the way a library adds its own handler around a logger it
// receives.
func Passthrough(l *slog.Logger) *slog.Logger {
	return slog.New(&passthrough{next: l.Handler()})
}

type passthrough struct {
	next slog.Handler
}

func (h *passthrough) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *passthrough) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

func (h *passthrough) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &passthrough{next: h.next.WithAttrs(attrs)}
}

func (h *passthrough) WithGroup(name string) slog.Handler {
	return &passthrough{next: h.next.WithGroup(name)}
}
