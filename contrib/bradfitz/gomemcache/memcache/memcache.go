// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package memcache provides functions to trace the bradfitz/gomemcache package (https://github.com/bradfitz/gomemcache).
//
// `WrapClient` will wrap a memcache `Client` and return a new struct with all
// the same methods, so should be seamless for existing applications. It also
// has an additional `WithContext` method which can be used to connect a span
// to an existing trace.
package memcache // import "github.com/DataDog/dd-trace-go/contrib/bradfitz/gomemcache/v2/memcache"

import (
	"context"
	"math"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = "bradfitz/gomemcache/memcache"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageBradfitzGoMemcache)
}

// WrapClient wraps a memcache.Client so that all requests are traced using the
// default tracer with the service name "memcached".
func WrapClient(client *memcache.Client, opts ...ClientOption) *Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/bradfitz/gomemcache/memcache: Wrapping Client: %#v", cfg)
	return &Client{
		Client:  client,
		cfg:     cfg,
		context: context.Background(),
	}
}

// A Client is used to trace requests to the memcached server.
type Client struct {
	*memcache.Client
	cfg     *clientConfig
	context context.Context
}

// WithContext creates a copy of the Client with the given context.
func (c *Client) WithContext(ctx context.Context) *Client {
	// the existing memcache client doesn't support context, but may in the
	// future, so we do a runtime check to detect this
	mc := c.Client
	if wc, ok := (interface{})(c.Client).(interface {
		WithContext(context.Context) *memcache.Client
	}); ok {
		mc = wc.WithContext(ctx)
	}
	return &Client{
		Client:  mc,
		cfg:     c.cfg,
		context: ctx,
	}
}

// startSpan starts a span from the context set with WithContext.
func (c *Client) startSpan(resourceName string) *tracer.Span {
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeMemcached),
		tracer.ServiceName(c.cfg.serviceName),
		tracer.ResourceName(resourceName),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemMemcached),
	}
	if !math.IsNaN(c.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, c.cfg.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(c.context, c.cfg.operationName, opts...)
	return span
}

// wrapped methods:

// Add invokes and traces Client.Add.
func (c *Client) Add(item *memcache.Item) error {
	span := c.startSpan("Add")
	err := c.Client.Add(item)
	span.Finish(tracer.WithError(err))
	return err
}

// Append invokes and traces Client.Append.
func (c *Client) Append(item *memcache.Item) error {
	span := c.startSpan("Append")
	err := c.Client.Append(item)
	span.Finish(tracer.WithError(err))
	return err
}

// CompareAndSwap invokes and traces Client.CompareAndSwap.
func (c *Client) CompareAndSwap(item *memcache.Item) error {
	span := c.startSpan("CompareAndSwap")
	err := c.Client.CompareAndSwap(item)
	span.Finish(tracer.WithError(err))
	return err
}

// Decrement invokes and traces Client.Decrement.
func (c *Client) Decrement(key string, delta uint64) (newValue uint64, err error) {
	span := c.startSpan("Decrement")
	newValue, err = c.Client.Decrement(key, delta)
	span.Finish(tracer.WithError(err))
	return newValue, err
}

// Delete invokes and traces Client.Delete.
func (c *Client) Delete(key string) error {
	span := c.startSpan("Delete")
	err := c.Client.Delete(key)
	span.Finish(tracer.WithError(err))
	return err
}

// DeleteAll invokes and traces Client.DeleteAll.
func (c *Client) DeleteAll() error {
	span := c.startSpan("DeleteAll")
	err := c.Client.DeleteAll()
	span.Finish(tracer.WithError(err))
	return err
}

// FlushAll invokes and traces Client.FlushAll.
func (c *Client) FlushAll() error {
	span := c.startSpan("FlushAll")
	err := c.Client.FlushAll()
	span.Finish(tracer.WithError(err))
	return err
}

// Get invokes and traces Client.Get.
func (c *Client) Get(key string) (item *memcache.Item, err error) {
	span := c.startSpan("Get")
	item, err = c.Client.Get(key)
	span.Finish(tracer.WithError(err))
	return item, err
}

// GetMulti invokes and traces Client.GetMulti.
func (c *Client) GetMulti(keys []string) (map[string]*memcache.Item, error) {
	span := c.startSpan("GetMulti")
	items, err := c.Client.GetMulti(keys)
	span.Finish(tracer.WithError(err))
	return items, err
}

// Increment invokes and traces Client.Increment.
func (c *Client) Increment(key string, delta uint64) (newValue uint64, err error) {
	span := c.startSpan("Increment")
	newValue, err = c.Client.Increment(key, delta)
	span.Finish(tracer.WithError(err))
	return newValue, err
}

// Prepend invokes and traces Client.Prepend.
func (c *Client) Prepend(item *memcache.Item) error {
	span := c.startSpan("Prepend")
	err := c.Client.Prepend(item)
	span.Finish(tracer.WithError(err))
	return err
}

// Replace invokes and traces Client.Replace.
func (c *Client) Replace(item *memcache.Item) error {
	span := c.startSpan("Replace")
	err := c.Client.Replace(item)
	span.Finish(tracer.WithError(err))
	return err
}

// Set invokes and traces Client.Set.
func (c *Client) Set(item *memcache.Item) error {
	span := c.startSpan("Set")
	err := c.Client.Set(item)
	span.Finish(tracer.WithError(err))
	return err
}

// Touch invokes and traces Client.Touch.
func (c *Client) Touch(key string, seconds int32) error {
	span := c.startSpan("Touch")
	err := c.Client.Touch(key, seconds)
	span.Finish(tracer.WithError(err))
	return err
}
