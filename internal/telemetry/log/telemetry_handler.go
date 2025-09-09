// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"log/slog"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// telemetryHandler adapts our telemetry system to work as a slog.Handler.
// This allows us to use slog's structured logging capabilities while
// maintaining our telemetry-specific requirements and security controls.
type telemetryHandler struct {
	opts []telemetry.LogOption
}

// NewTelemetryHandler creates a slog.Handler that sends logs to the telemetry system.
// The provided LogOptions will be applied to all log messages sent through this handler.
func NewTelemetryHandler(opts ...telemetry.LogOption) slog.Handler {
	return &telemetryHandler{
		opts: opts,
	}
}

// Enabled reports whether the handler handles records at the given level.
// For telemetry, we generally want to handle all levels, but this could be
// made configurable in the future based on telemetry settings.
func (h *telemetryHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// For now, we accept all levels. In the future, this could check
	// telemetry configuration to determine if a level should be processed.
	switch level {
	case slog.LevelDebug:
		return true // Could check if debug telemetry is enabled
	case slog.LevelInfo:
		return true // Info is typically always enabled
	case slog.LevelWarn:
		return true // Warnings are typically always enabled
	case slog.LevelError:
		return true // Errors are always enabled
	default:
		// For custom log levels, we'll be conservative and handle them
		return true
	}
}

// Handle handles the Record by sending it through the telemetry system.
// Attributes remain structured in the Record for proper slog integration.
func (h *telemetryHandler) Handle(ctx context.Context, r slog.Record) error {
	telemetry.Log(r, h.opts...)
	return nil
}

// WithAttrs returns a new Handler whose attributes consist of
// both the receiver's attributes and the arguments.
// For telemetry, we convert slog.Attr to our LogOption format.
func (h *telemetryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	// Convert slog.Attr to telemetry.LogOption
	newOpts := make([]telemetry.LogOption, 0, len(h.opts)+len(attrs))
	newOpts = append(newOpts, h.opts...)

	// Convert each slog.Attr to appropriate telemetry options
	for _, attr := range attrs {
		// For now, we'll add them as tags. In the future, we could
		// support different types of attributes differently.
		newOpts = append(newOpts, telemetry.WithTags([]string{attr.Key + ":" + attr.Value.String()}))
	}

	return &telemetryHandler{opts: newOpts}
}

// WithGroup returns a new Handler with the given group appended to
// the receiver's existing groups. For telemetry, we'll prefix
// attribute keys with the group name.
func (h *telemetryHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	// For now, we'll create a simple implementation that prefixes
	// future attributes with the group name. A more sophisticated
	// implementation could maintain a group hierarchy.
	return &telemetryHandlerWithGroup{
		handler:   h,
		groupName: name,
	}
}

// telemetryHandlerWithGroup wraps a telemetryHandler to add group support
type telemetryHandlerWithGroup struct {
	handler   *telemetryHandler
	groupName string
}

func (h *telemetryHandlerWithGroup) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *telemetryHandlerWithGroup) Handle(ctx context.Context, r slog.Record) error {
	// For grouped handlers, we could modify the record to include group context
	// For now, we'll delegate to the underlying handler
	return h.handler.Handle(ctx, r)
}

func (h *telemetryHandlerWithGroup) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Prefix attribute keys with the group name
	prefixedAttrs := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		prefixedAttrs[i] = slog.Attr{
			Key:   h.groupName + "." + attr.Key,
			Value: attr.Value,
		}
	}

	return &telemetryHandlerWithGroup{
		handler:   h.handler.WithAttrs(prefixedAttrs).(*telemetryHandler),
		groupName: h.groupName,
	}
}

func (h *telemetryHandlerWithGroup) WithGroup(name string) slog.Handler {
	// Combine group names
	combinedName := h.groupName + "." + name
	return &telemetryHandlerWithGroup{
		handler:   h.handler,
		groupName: combinedName,
	}
}
