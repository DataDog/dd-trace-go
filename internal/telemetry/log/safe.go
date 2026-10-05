// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"log/slog"
	"reflect"
	"strconv"
)

// SafeError represents a sanitized error for secure telemetry logging.
// It only exposes the error type, never the error message, to prevent PII leakage.
type SafeError struct {
	errType string
}

const (
	nilErrorType = "<nil>"
)

// NewSafeError creates a SafeError from a regular error
func NewSafeError(err error) SafeError {
	if err == nil {
		return SafeError{errType: nilErrorType}
	}

	return SafeError{
		errType: errorType(err),
	}
}

// LogValue implements slog.LogValuer to provide secure logging representation
func (e SafeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("error_type", e.errType),
	)
}

// errorType extracts a value's type without exposing its content. Despite the
// name, v need not implement error — ReportPanic reuses it for non-error
// recovered panic values, which carry the same "type is safe, content is not"
// disclosure rule as errors.
func errorType(v any) string {
	if v == nil {
		return nilErrorType
	}

	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if t.Name() == "" {
		// Unnamed types (anonymous structs, slices, maps, channels, funcs)
		// have no Name()/PkgPath(); String() still describes the type
		// (e.g. "[]string", "struct { X int }") without exposing values.
		return t.String()
	}
	if t.PkgPath() != "" {
		return t.PkgPath() + "." + t.Name()
	}
	return t.Name()
}

// SafeSlice provides secure logging for slice/array types
type SafeSlice struct {
	items     []string
	count     int
	truncated bool
}

// NewSafeSlice creates a SafeSlice from any slice, converting items to strings
func NewSafeSlice[T any](items []T) SafeSlice {
	return NewSafeSliceWithLimit(items, 100)
}

// NewSafeSliceWithLimit creates a SafeSlice with custom item limit
func NewSafeSliceWithLimit[T any](items []T, maxItems int) SafeSlice {
	stringItems := make([]string, 0, min(len(items), maxItems))

	for i, item := range items {
		if i >= maxItems {
			break
		}

		// Convert item to string safely - only explicit conversions allowed
		var str string
		switch v := any(item).(type) {
		case string:
			str = v
		case int:
			str = strconv.Itoa(v)
		case int64:
			str = strconv.FormatInt(v, 10)
		case bool:
			str = strconv.FormatBool(v)
		case float64:
			str = strconv.FormatFloat(v, 'g', -1, 64)
		default:
			str = "<unsupported-type>"
		}
		stringItems = append(stringItems, str)
	}

	return SafeSlice{
		items:     stringItems,
		count:     len(items),
		truncated: len(items) > maxItems,
	}
}

// LogValue implements slog.LogValuer for secure slice logging
func (s SafeSlice) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.Any("items", s.items),
	}

	if s.truncated {
		attrs = append(attrs, slog.Bool("truncated", true), slog.Int("count", s.count))
	}

	return slog.GroupValue(attrs...)
}
