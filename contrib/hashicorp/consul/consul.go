package consul

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	consul "github.com/hashicorp/consul/api"
)

// Client wraps the regular *consul.Client and augments it with tracing. Use NewClient to initialize it.
type Client struct {
	*consul.Client

	config *clientConfig
	ctx    context.Context
}

// NewClient returns a traced Consul client.
func NewClient(config *consul.Config, opts ...ClientOption) (*Client, error) {
	c, err := consul.NewClient(config)
	if err != nil {
		return nil, err
	}
	return WrapClient(c, opts...), nil
}

// WrapClient wraps a given consul.Client with a tracer under the given service name.
func WrapClient(c *consul.Client, opts ...ClientOption) *Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &Client{c, cfg, context.Background()}
}

// WithContext sets a context on a Client. Use it to ensure that emitted spans have the correct parent.
func (c *Client) WithContext(ctx context.Context) *Client {
	c.ctx = ctx
	return c
}

// A KV is used to trace requests to Consul's KV.
type KV struct {
	*consul.KV

	config *clientConfig
	ctx    context.Context
}

// KV returns the KV for the Client.
func (c *Client) KV() *KV {
	return &KV{c.Client.KV(), c.config, c.ctx}
}

func (k *KV) startSpan(resourceName string, key string) ddtrace.Span {
	opts := []ddtrace.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.ServiceName(k.config.serviceName),
		tracer.SpanType(ext.SpanTypeConsul),
		tracer.Tag("consul.key", key),
	}
	if !math.IsNaN(k.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, k.config.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(k.ctx, k.config.spanName, opts...)
	return span
}

// Put is used to write a new value. Only the
// Key, Flags and Value is respected.
func (k *KV) Put(p *consul.KVPair, q *consul.WriteOptions) (*consul.WriteMeta, error) {
	span := k.startSpan("PUT", p.Key)
	meta, err := k.KV.Put(p, q)
	defer span.Finish(tracer.WithError(err))
	return meta, err
}

// Get is used to lookup a single key. The returned pointer
// to the KVPair will be nil if the key does not exist.
func (k *KV) Get(key string, q *consul.QueryOptions) (*consul.KVPair, *consul.QueryMeta, error) {
	span := k.startSpan("GET", key)
	pair, meta, err := k.KV.Get(key, q)
	defer span.Finish(tracer.WithError(err))
	return pair, meta, err
}

// List is used to lookup all keys under a prefix.
func (k *KV) List(prefix string, q *consul.QueryOptions) ([]*consul.KVPair, *consul.QueryMeta, error) {
	span := k.startSpan("LIST", prefix)
	pairs, meta, err := k.KV.List(prefix, q)
	defer span.Finish(tracer.WithError(err))
	return pairs, meta, err
}

// Keys is used to list all the keys under a prefix. Optionally,
// a separator can be used to limit the responses.
func (k *KV) Keys(prefix, separator string, q *consul.QueryOptions) ([]string, *consul.QueryMeta, error) {
	span := k.startSpan("KEYS", prefix)
	entries, meta, err := k.KV.Keys(prefix, separator, q)
	defer span.Finish(tracer.WithError(err))
	return entries, meta, err
}

// CAS is used for a Check-And-Set operation. The Key,
// ModifyIndex, Flags and Value are respected. Returns true
// on success or false on failures.
func (k *KV) CAS(p *consul.KVPair, q *consul.WriteOptions) (bool, *consul.WriteMeta, error) {
	span := k.startSpan("CAS", p.Key)
	r, meta, err := k.KV.CAS(p, q)
	defer span.Finish(tracer.WithError(err))
	return r, meta, err
}

// Acquire is used for a lock acquisition operation. The Key,
// Flags, Value and Session are respected. Returns true
// on success or false on failures.
func (k *KV) Acquire(p *consul.KVPair, q *consul.WriteOptions) (bool, *consul.WriteMeta, error) {
	span := k.startSpan("ACQUIRE", p.Key)
	r, meta, err := k.KV.Acquire(p, q)
	defer span.Finish(tracer.WithError(err))
	return r, meta, err
}

// Release is used for a lock release operation. The Key,
// Flags, Value and Session are respected. Returns true
// on success or false on failures.
func (k *KV) Release(p *consul.KVPair, q *consul.WriteOptions) (bool, *consul.WriteMeta, error) {
	span := k.startSpan("RELEASE", p.Key)
	r, meta, err := k.KV.Release(p, q)
	defer span.Finish(tracer.WithError(err))
	return r, meta, err
}

// Delete is used to delete a single key.
func (k *KV) Delete(key string, w *consul.WriteOptions) (*consul.WriteMeta, error) {
	span := k.startSpan("DELETE", key)
	meta, err := k.KV.Delete(key, w)
	defer span.Finish(tracer.WithError(err))
	return meta, err
}

// DeleteCAS is used for a Delete Check-And-Set operation. The Key
// and ModifyIndex are respected. Returns true on success or false on failures.
func (k *KV) DeleteCAS(p *consul.KVPair, q *consul.WriteOptions) (bool, *consul.WriteMeta, error) {
	span := k.startSpan("DELETECAS", p.Key)
	r, meta, err := k.KV.DeleteCAS(p, q)
	defer span.Finish(tracer.WithError(err))
	return r, meta, err
}

// DeleteTree is used to delete all keys under a prefix.
func (k *KV) DeleteTree(prefix string, w *consul.WriteOptions) (*consul.WriteMeta, error) {
	span := k.startSpan("DELETETREE", prefix)
	meta, err := k.KV.DeleteTree(prefix, w)
	defer span.Finish(tracer.WithError(err))
	return meta, err
}
