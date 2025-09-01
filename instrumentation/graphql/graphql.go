// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package graphql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// ErrorExtensionsFromEnv returns the configured error extensions from an environment variable.
func ErrorExtensionsFromEnv() []string {
	s := env.Get("DD_TRACE_GRAPHQL_ERROR_EXTENSIONS")
	if s == "" {
		return nil
	}
	return ParseErrorExtensions(strings.Split(s, ","))
}

// ParseErrorExtensions validates and cleans up the user provider error extension list.
func ParseErrorExtensions(errExtensions []string) []string {
	var res []string
	for _, v := range errExtensions {
		cleanupVal := strings.TrimSpace(v)
		if cleanupVal != "" {
			res = append(res, cleanupVal)
		}
	}
	slices.Sort(res)
	return slices.Compact(res)
}

// Error represents a generic Graphql error.
type Error struct {
	OriginalErr error
	Message     string
	Locations   []ErrorLocation
	Path        []any
	Extensions  map[string]any
}

// ErrorLocation represents each individual member of the locations field of a Graphql error.
type ErrorLocation struct {
	Line   int
	Column int
}

// AddErrorsAsSpanEvents attaches the given graphql errors to the span as span events, according to the standard
// Datadog specification.
// errExtensions allows to include the given extensions field from the error as attributes in the span events.
func AddErrorsAsSpanEvents(span *tracer.Span, errs []Error, errExtensions []string) {
	ts := time.Now()
	for _, err := range errs {
		span.AddEvent(
			ext.GraphqlQueryErrorEvent,
			tracer.WithSpanEventTimestamp(ts),
			tracer.WithSpanEventAttributes(errToSpanEventAttributes(err, errExtensions)),
		)
	}
}

func errToSpanEventAttributes(gErr Error, errExtensions []string) map[string]any {
	res := map[string]any{
		"message":    gErr.Message,
		"type":       reflect.TypeOf(gErr.OriginalErr).String(),
		"stacktrace": takeStacktrace(0, 0),
	}
	if locs := parseErrLocations(gErr.Locations); len(locs) > 0 {
		res["locations"] = locs
	}
	if errPath := parseErrPath(gErr.Path); len(errPath) > 0 {
		res["path"] = errPath
	}
	setErrExtensions(res, gErr.Extensions, errExtensions)
	return res
}

func parseErrLocations(locs []ErrorLocation) []string {
	res := make([]string, 0, len(locs))
	for _, loc := range locs {
		res = append(res, fmt.Sprintf("%d:%d", loc.Line, loc.Column))
	}
	return res
}

func parseErrPath(p []any) []string {
	res := make([]string, 0, len(p))
	for _, v := range p {
		res = append(res, fmt.Sprintf("%v", v))
	}
	return res
}

func setErrExtensions(result map[string]any, extensions map[string]any, whitelist []string) {
	for _, errExt := range whitelist {
		val, ok := extensions[errExt]
		if !ok {
			continue
		}
		key := fmt.Sprintf("extensions.%s", errExt)
		mapVal, err := errExtensionMapValue(val)
		if err != nil {
			log.Debug("failed to set error extension as span event attribute %q: %v", errExt, err.Error())
			continue
		}
		result[key] = mapVal
	}
}

func errExtensionMapValue(val any) (any, error) {
	switch v := val.(type) {
	case string, bool, int, uint, int64, uint64, uint8, uint16, uint32, uintptr, int8, int16, int32, float64, float32,
		[]string, []bool, []int, []uint, []int64, []uint64, []uint8, []uint16, []uint32, []uintptr, []int8, []int16, []int32, []float64, []float32:
		return v, nil
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	}
}

// defaultStackLength specifies the default maximum size of a stack trace.
const defaultStackLength = 32

// takeStacktrace takes a stack trace of maximum n entries, skipping the first skip entries.
// This function is the same as ddtrace/tracer/span.go
func takeStacktrace(n, skip uint) string {
	if n == 0 {
		n = defaultStackLength
	}
	var builder strings.Builder
	pcs := make([]uintptr, n)

	// +2 to exclude runtime.Callers and takeStacktrace
	numFrames := runtime.Callers(2+int(skip), pcs)
	if numFrames == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs[:numFrames])
	for i := 0; ; i++ {
		frame, more := frames.Next()
		if i != 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(frame.Function)
		builder.WriteByte('\n')
		builder.WriteByte('\t')
		builder.WriteString(frame.File)
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(frame.Line))
		if !more {
			break
		}
	}
	return builder.String()
}
