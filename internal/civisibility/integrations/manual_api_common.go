// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"sync"
	"time"
	_ "unsafe" // for go:linkname

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

// Go linknames

//go:linkname getMeta github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.getMeta
func getMeta(s *tracer.Span, key string) (string, bool)

//go:linkname getMetric github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.getMetric
func getMetric(s *tracer.Span, key string) (float64, bool)

// common
var _ ddTslvEvent = (*ciVisibilityCommon)(nil)

// ciVisibilityCommon is a struct that implements the ddTslvEvent interface and provides common functionality for CI visibility.
type ciVisibilityCommon struct {
	mutex     sync.Mutex
	startTime time.Time

	tags   []tracer.StartSpanOption
	span   *tracer.Span
	closed bool

	ctxMutex sync.Mutex
	ctx      context.Context
}

// Context returns the context of the event.
func (c *ciVisibilityCommon) Context() context.Context {
	c.ctxMutex.Lock()
	defer c.ctxMutex.Unlock()
	return c.ctx
}

// StartTime returns the start time of the event.
func (c *ciVisibilityCommon) StartTime() time.Time { return c.startTime }

// SetError sets an error on the event.
func (c *ciVisibilityCommon) SetError(options ...ErrorOption) {
	defaults := &tslvErrorOptions{}
	for _, o := range options {
		o(defaults)
	}

	// if there is an error, set the span with the error
	if defaults.err != nil {
		c.span.SetTag(ext.Error, defaults.err)
		return
	}

	// if there is no error, set the span with error the error info

	// set the span with error:1
	c.span.SetTag(ext.Error, true)

	// set the error type
	if defaults.errType != "" {
		c.span.SetTag(ext.ErrorType, defaults.errType)
	}

	// set the error message
	if defaults.message != "" {
		c.span.SetTag(ext.ErrorMsg, defaults.message)
	}

	// set the error stacktrace
	if defaults.callstack != "" {
		c.span.SetTag(ext.ErrorStack, defaults.callstack)
	}
}

// SetTag sets a tag on the event.
func (c *ciVisibilityCommon) SetTag(key string, value interface{}) { c.span.SetTag(key, value) }

// GetTag retrieves a tag from the event.
func (c *ciVisibilityCommon) GetTag(key string) (interface{}, bool) {
	// Check if the span is nil
	if c.span == nil {
		return nil, false
	}

	// Check if the key is a meta key
	metaVal, ok := getMeta(c.span, key)
	if ok {
		return metaVal, true
	}

	// Check if the key is a metric key
	metricVal, ok := getMetric(c.span, key)
	return metricVal, ok
}

// fillCommonTags adds common tags to the span options for CI visibility.
func fillCommonTags(opts []tracer.StartSpanOption) []tracer.StartSpanOption {
	opts = append(opts, []tracer.StartSpanOption{
		tracer.Tag(constants.Origin, constants.CIAppTestOrigin),
		tracer.Tag(ext.ManualKeep, true),
	}...)

	// Apply CI tags
	for k, v := range utils.GetCITags() {
		// Ignore the test session name (sent at the payload metadata level, see `civisibility_payload.go`)
		if k == constants.TestSessionName {
			continue
		}
		opts = append(opts, tracer.Tag(k, v))
	}

	// Apply CI metrics
	for k, v := range utils.GetCIMetrics() {
		opts = append(opts, tracer.Tag(k, v))
	}

	return opts
}

func (c *ciVisibilityCommon) getContextValue(key any) any {
	c.ctxMutex.Lock()
	defer c.ctxMutex.Unlock()
	return c.ctx.Value(key)
}

func (c *ciVisibilityCommon) setContextValue(key, value any) {
	c.ctxMutex.Lock()
	defer c.ctxMutex.Unlock()
	c.ctx = context.WithValue(c.ctx, key, value)
}
