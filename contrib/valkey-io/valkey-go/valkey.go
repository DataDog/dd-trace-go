// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package valkey provides tracing functions for tracing the valkey-io/valkey-go package (https://github.com/valkey-io/valkey-go).
package valkey

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageValkeyIoValkeyGo)
}

var (
	_ valkey.Client = (*client)(nil)
)

type client struct {
	client  valkey.Client
	cfg     *config
	host    string
	port    string
	dbIndex string
	user    string
}

func (c *client) B() valkey.Builder {
	return c.client.B()
}

func (c *client) Close() {
	c.client.Close()
}

// NewClient returns a new valkey.Client enhanced with tracing.
func NewClient(clientOption valkey.ClientOption, opts ...Option) (valkey.Client, error) {
	valkeyClient, err := valkey.NewClient(clientOption)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	for _, fn := range opts {
		fn(cfg)
	}
	tClient := &client{
		client:  valkeyClient,
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

func (c *client) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	span, ctx := c.startSpan(ctx, processCommand(&cmd))
	resp := c.client.Do(ctx, cmd)
	setClientCacheTags(span, resp)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
	info := buildPipelineInfo(countCommandNames(multi))
	span, ctx := c.startSpan(ctx, command{statement: info.resource})
	setValkeyPipelineTags(span, info)
	resp := c.client.DoMulti(ctx, multi...)
	c.finishSpan(span, c.firstError(resp))
	return resp
}

func (c *client) Receive(ctx context.Context, subscribe valkey.Completed, fn func(msg valkey.PubSubMessage)) error {
	span, ctx := c.startSpan(ctx, processCommand(&subscribe))
	err := c.client.Receive(ctx, subscribe, fn)
	c.finishSpan(span, err)
	return err
}

func (c *client) DoCache(ctx context.Context, cmd valkey.Cacheable, ttl time.Duration) valkey.ValkeyResult {
	span, ctx := c.startSpan(ctx, processCommand(&cmd))
	resp := c.client.DoCache(ctx, cmd, ttl)
	setClientCacheTags(span, resp)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) DoMultiCache(ctx context.Context, multi ...valkey.CacheableTTL) []valkey.ValkeyResult {
	info := buildPipelineInfo(countCacheableCommandNames(multi))
	span, ctx := c.startSpan(ctx, command{statement: info.resource})
	setValkeyPipelineTags(span, info)
	resp := c.client.DoMultiCache(ctx, multi...)
	c.finishSpan(span, c.firstError(resp))
	return resp
}

func (c *client) DoStream(ctx context.Context, cmd valkey.Completed) (resp valkey.ValkeyResultStream) {
	span, ctx := c.startSpan(ctx, processCommand(&cmd))
	resp = c.client.DoStream(ctx, cmd)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) DoMultiStream(ctx context.Context, multi ...valkey.Completed) valkey.MultiValkeyResultStream {
	info := buildPipelineInfo(countCommandNames(multi))
	span, ctx := c.startSpan(ctx, command{statement: info.resource})
	setValkeyPipelineTags(span, info)
	resp := c.client.DoMultiStream(ctx, multi...)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *client) Dedicated(fn func(valkey.DedicatedClient) error) error {
	return c.client.Dedicated(func(dc valkey.DedicatedClient) error {
		return fn(&dedicatedClient{
			client:          c,
			dedicatedClient: dc,
		})
	})
}

func (c *client) Dedicate() (client valkey.DedicatedClient, cancel func()) {
	dedicated, cancel := c.client.Dedicate()
	return &dedicatedClient{
		client:          c,
		dedicatedClient: dedicated,
	}, cancel
}

func (c *client) Nodes() map[string]valkey.Client {
	nodes := c.client.Nodes()
	for addr, valkeyClient := range nodes {
		host, port, _ := net.SplitHostPort(addr)
		nodes[addr] = &client{
			client:  valkeyClient,
			cfg:     c.cfg,
			host:    host,
			port:    port,
			dbIndex: c.dbIndex,
			user:    c.user,
		}
	}
	return nodes
}

func (c *client) Mode() valkey.ClientMode {
	return c.client.Mode()
}

var (
	_ valkey.DedicatedClient = (*dedicatedClient)(nil)
)

type dedicatedClient struct {
	*client
	dedicatedClient valkey.DedicatedClient
}

func (c *dedicatedClient) SetPubSubHooks(hooks valkey.PubSubHooks) <-chan error {
	return c.dedicatedClient.SetPubSubHooks(hooks)
}

func (c *dedicatedClient) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	span, ctx := c.startSpan(ctx, processCommand(&cmd))
	resp := c.dedicatedClient.Do(ctx, cmd)
	setClientCacheTags(span, resp)
	c.finishSpan(span, resp.Error())
	return resp
}

func (c *dedicatedClient) DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
	info := buildPipelineInfo(countCommandNames(multi))
	span, ctx := c.startSpan(ctx, command{statement: info.resource})
	setValkeyPipelineTags(span, info)
	resp := c.dedicatedClient.DoMulti(ctx, multi...)
	c.finishSpan(span, c.firstError(resp))
	return resp
}

func (c *dedicatedClient) Receive(ctx context.Context, subscribe valkey.Completed, fn func(msg valkey.PubSubMessage)) error {
	span, ctx := c.startSpan(ctx, processCommand(&subscribe))
	err := c.dedicatedClient.Receive(ctx, subscribe, fn)
	c.finishSpan(span, err)
	return err
}

func (c *dedicatedClient) Close() {
	c.dedicatedClient.Close()
}

type command struct {
	statement string
	raw       string
}

func (c *client) startSpan(ctx context.Context, cmd command) (*tracer.Span, context.Context) {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(c.cfg.serviceName),
		tracer.ResourceName(cmd.statement),
		tracer.SpanType(ext.SpanTypeValkey),
		tracer.Tag(ext.TargetHost, c.host),
		tracer.Tag(ext.TargetPort, c.port),
		tracer.Tag(ext.Component, instrumentation.PackageValkeyIoValkeyGo),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemValkey),
		tracer.Tag(ext.TargetDB, c.dbIndex),
	}
	if c.cfg.rawCommand && cmd.raw != "" {
		opts = append(opts, tracer.Tag(ext.ValkeyRawCommand, cmd.raw))
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
	return tracer.StartSpanFromContext(ctx, "valkey.command", opts...)
}

func (c *client) finishSpan(span *tracer.Span, err error) {
	var opts []tracer.FinishOption
	if c.cfg.errCheck(err) {
		opts = append(opts, tracer.WithError(err))
	}
	span.Finish(opts...)
}

func (c *client) firstError(s []valkey.ValkeyResult) error {
	for _, result := range s {
		if err := result.Error(); c.cfg.errCheck(err) {
			return err
		}
	}
	return nil
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

type pipelineInfo struct {
	resource string // deduplicated command names sorted alphabetically (e.g. "DEL GET SET")
	commands string // command count breakdown (e.g. "DEL:1 GET:2 SET:3")
	length   int    // total number of commands
}

func buildPipelineInfo(counts map[string]int) pipelineInfo {
	names := make([]string, 0, len(counts))
	total := 0
	for name, count := range counts {
		names = append(names, name)
		total += count
	}
	sort.Strings(names)
	resource := strings.Builder{}
	commands := strings.Builder{}
	for i, name := range names {
		if i > 0 {
			resource.WriteString(" ")
			commands.WriteString(" ")
		}
		resource.WriteString(name)
		commands.WriteString(name)
		commands.WriteString(":")
		commands.WriteString(strconv.Itoa(counts[name]))
	}
	return pipelineInfo{
		resource: resource.String(),
		commands: commands.String(),
		length:   total,
	}
}

func setValkeyPipelineTags(span *tracer.Span, info pipelineInfo) {
	span.SetTag(ext.ValkeyPipelineLength, info.length)
	span.SetTag(ext.ValkeyPipelineCommandCounts, info.commands)
}

func countCommandNames(multi []valkey.Completed) map[string]int {
	counts := make(map[string]int, len(multi))
	for _, cmd := range multi {
		cmds := cmd.Commands()
		if len(cmds) > 0 {
			counts[cmds[0]]++
		}
	}
	return counts
}

func countCacheableCommandNames(multi []valkey.CacheableTTL) map[string]int {
	counts := make(map[string]int, len(multi))
	for _, cmd := range multi {
		cmds := cmd.Cmd.Commands()
		if len(cmds) > 0 {
			counts[cmds[0]]++
		}
	}
	return counts
}

func setClientCacheTags(s *tracer.Span, result valkey.ValkeyResult) {
	s.SetTag(ext.ValkeyClientCacheHit, result.IsCacheHit())
	s.SetTag(ext.ValkeyClientCacheTTL, result.CacheTTL())
	s.SetTag(ext.ValkeyClientCachePTTL, result.CachePTTL())
	s.SetTag(ext.ValkeyClientCachePXAT, result.CachePXAT())
}
