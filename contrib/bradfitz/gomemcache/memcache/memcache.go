package memcache

import (
	"context"

	"github.com/bradfitz/gomemcache/memcache"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// WrapClient wraps a memcache.Client so that all requests are traced using the
// default tracer with the service name "memcached".
func WrapClient(client *memcache.Client, opts ...ClientOption) *Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
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
	if hasWithContext, ok := (interface{})(c.Client).(interface {
		WithContext(context.Context) *memcache.Client
	}); ok {
		mc = hasWithContext.WithContext(ctx)
	}
	return &Client{
		Client:  mc,
		cfg:     c.cfg,
		context: ctx,
	}
}

// startSpan starts a span from the context set with WithContext.
func (c *Client) startSpan(resourceName string) ddtrace.Span {
	span, _ := tracer.StartSpanFromContext(c.context, operationName,
		tracer.ServiceName(c.cfg.serviceName),
		tracer.ResourceName(resourceName))
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
