// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

// Package rueidis provides tracing functions for tracing the redis/rueidis package (https://github.com/redis/rueidis).
package rueidis

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/rueidis"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageRedisRueidis)
}

var (
	_ rueidis.Client          = (*client)(nil)
	_ rueidis.DedicatedClient = (*dedicatedClient)(nil)
)

// NewClient returns a new rueidis.Client enhanced with tracing.
func NewClient(clientOption rueidis.ClientOption, opts ...Option) (rueidis.Client, error) {
	rueidisClient, err := rueidis.NewClient(clientOption)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	for _, fn := range opts {
		fn(cfg)
	}
	tClient := &client{
		client:  rueidisClient,
		cfg:     cfg,
		dbIndex: strconv.FormatInt(int64(clientOption.SelectDB), 10),
		user:    clientOption.Username,
	}
	if len(clientOption.InitAddress) > 0 {
		host, port, err := net.SplitHostPort(clientOption.InitAddress[0])
		if err == nil {
			tClient.host = host
			tClient.port = port
		}
	}
	return tClient, nil
}

type client struct {
	client  rueidis.Client
	cfg     *config
	host    string
	port    string
	dbIndex string
	user    string
}

type command struct {
	statement string
	raw       string
}

func (c *client) startSpan(ctx context.Context, cmd command) (*tracer.Span, context.Context) {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(c.cfg.serviceName),
		tracer.ResourceName(cmd.statement),
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.Tag(ext.TargetHost, c.host),
		tracer.Tag(ext.TargetPort, c.port),
		tracer.Tag(ext.Component, instrumentation.PackageRedisRueidis),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
		tracer.Tag(ext.TargetDB, c.dbIndex),
	}
	if c.cfg.rawCommand {
		opts = append(opts, tracer.Tag(ext.RedisRawCommand, cmd.raw))
	}
	if c.host != "" {
		opts = append(opts, tracer.Tag(ext.TargetHost, c.host))
	}
	if c.port != "" {
		opts = append(opts, tracer.Tag(ext.TargetPort, c.port))
	}
	if c.user != "" {
		opts = append(opts, tracer.Tag(ext.DBUser, c.user))
	}
	return tracer.StartSpanFromContext(ctx, "redis.command", opts...)
}

func (c *client) finishSpan(span *tracer.Span, err error) {
	var opts []tracer.FinishOption
	if c.cfg.errCheck(err) {
		opts = append(opts, tracer.WithError(err))
	}
	span.Finish(opts...)
}

func (c *client) firstError(s []rueidis.RedisResult) error {
	for _, result := range s {
		if err := result.Error(); c.cfg.errCheck(err) {
			return err
		}
	}
	return nil
}

func (c *client) B() rueidis.Builder {
	return c.client.B()
}

func (c *client) Do(ctx context.Context, cmd rueidis.Completed) rueidis.RedisResult {
	span, ctx := c.startSpan(ctx, processCommand(&cmd))
	resp := c.client.Do(ctx, cmd)
	setClientCacheTags(span, resp)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) DoMulti(ctx context.Context, multi ...rueidis.Completed) []rueidis.RedisResult {
	span, ctx := c.startSpan(ctx, processCommandMulti(multi))
	resp := c.client.DoMulti(ctx, multi...)
	c.finishSpan(span, c.firstError(resp))
	return resp
}

func (c *client) Receive(ctx context.Context, subscribe rueidis.Completed, fn func(msg rueidis.PubSubMessage)) error {
	span, ctx := c.startSpan(ctx, processCommand(&subscribe))
	err := c.client.Receive(ctx, subscribe, fn)
	c.finishSpan(span, err)
	return err
}

func (c *client) Close() {
	c.client.Close()
}

func (c *client) DoCache(ctx context.Context, cmd rueidis.Cacheable, ttl time.Duration) rueidis.RedisResult {
	span, ctx := c.startSpan(ctx, processCommand(&cmd))
	resp := c.client.DoCache(ctx, cmd, ttl)
	setClientCacheTags(span, resp)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) DoMultiCache(ctx context.Context, multi ...rueidis.CacheableTTL) []rueidis.RedisResult {
	span, ctx := c.startSpan(ctx, processCommandMultiCache(multi))
	resp := c.client.DoMultiCache(ctx, multi...)
	c.finishSpan(span, c.firstError(resp))
	return resp
}

func (c *client) DoStream(ctx context.Context, cmd rueidis.Completed) rueidis.RedisResultStream {
	span, ctx := c.startSpan(ctx, processCommand(&cmd))
	resp := c.client.DoStream(ctx, cmd)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) DoMultiStream(ctx context.Context, multi ...rueidis.Completed) rueidis.MultiRedisResultStream {
	span, ctx := c.startSpan(ctx, processCommandMulti(multi))
	resp := c.client.DoMultiStream(ctx, multi...)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) Dedicated(fn func(rueidis.DedicatedClient) error) (err error) {
	return c.client.Dedicated(func(dc rueidis.DedicatedClient) error {
		return fn(&dedicatedClient{
			client:          c,
			dedicatedClient: dc,
		})
	})
}

func (c *client) Dedicate() (client rueidis.DedicatedClient, cancel func()) {
	dedicated, cancel := c.client.Dedicate()
	return &dedicatedClient{
		client:          c,
		dedicatedClient: dedicated,
	}, cancel
}

func (c *client) Nodes() map[string]rueidis.Client {
	nodes := c.client.Nodes()
	for addr, redisClient := range nodes {
		host, port, _ := net.SplitHostPort(addr)
		nodes[addr] = &client{
			client:  redisClient,
			cfg:     c.cfg,
			host:    host,
			port:    port,
			dbIndex: c.dbIndex,
			user:    c.user,
		}
	}
	return nodes
}

func (c *client) Mode() rueidis.ClientMode {
	return c.client.Mode()
}

type dedicatedClient struct {
	*client
	dedicatedClient rueidis.DedicatedClient
}

func (c *dedicatedClient) SetPubSubHooks(hooks rueidis.PubSubHooks) <-chan error {
	return c.dedicatedClient.SetPubSubHooks(hooks)
}

type commander interface {
	Commands() []string
}

func processCommand(cmd commander) command {
	cmds := cmd.Commands()
	if len(cmds) == 0 {
		return command{}
	}
	statement := cmds[0]
	raw := strings.Join(cmds, " ")
	return command{
		statement: statement,
		raw:       raw,
	}
}

func processCommandMulti(multi []rueidis.Completed) command {
	var cmds []command
	for _, cmd := range multi {
		cmds = append(cmds, processCommand(&cmd))
	}
	return multiCommand(cmds)
}

func processCommandMultiCache(multi []rueidis.CacheableTTL) command {
	var cmds []command
	for _, cmd := range multi {
		cmds = append(cmds, processCommand(&cmd.Cmd))
	}
	return multiCommand(cmds)
}

func multiCommand(cmds []command) command {
	// limit to the 5 first
	if len(cmds) > 5 {
		cmds = cmds[:5]
	}
	statement := strings.Builder{}
	raw := strings.Builder{}
	for i, cmd := range cmds {
		statement.WriteString(cmd.statement)
		raw.WriteString(cmd.raw)
		if i != len(cmds)-1 {
			statement.WriteString(" ")
			raw.WriteString(" ")
		}
	}
	return command{
		statement: statement.String(),
		raw:       raw.String(),
	}
}

func setClientCacheTags(s *tracer.Span, result rueidis.RedisResult) {
	s.SetTag(ext.RedisClientCacheHit, result.IsCacheHit())
	s.SetTag(ext.RedisClientCacheTTL, result.CacheTTL())
	s.SetTag(ext.RedisClientCachePTTL, result.CachePTTL())
	s.SetTag(ext.RedisClientCachePXAT, result.CachePXAT())
}
