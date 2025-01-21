// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package valkey provides tracing functions for tracing the valkey-io/valkey-go package (https://github.com/valkey-io/valkey-go).
package valkey

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "valkey-io/valkey-go"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/valkey-io/valkey-go")
}

var (
	_ valkey.CoreClient      = (*coreClient)(nil)
	_ valkey.Client          = (*client)(nil)
	_ valkey.DedicatedClient = (*dedicatedClient)(nil)
)

type coreClient struct {
	valkey.Client
	option       valkey.ClientOption
	clientConfig clientConfig
	spanName     string
	host         string
	port         int
}

type client struct {
	coreClient
}

type dedicatedClient struct {
	coreClient
	dedicatedClient valkey.DedicatedClient
}

func NewClient(option valkey.ClientOption, opts ...ClientOption) (valkey.Client, error) {
	valkeyClient, err := valkey.NewClient(option)
	if err != nil {
		return nil, err
	}
	var cfg clientConfig
	defaults(&cfg)
	for _, fn := range opts {
		fn(&cfg)
	}
	var host string
	var port int
	if len(option.InitAddress) == 1 {
		host, port = splitHostPort(option.InitAddress[0])
	}
	core := coreClient{
		Client:       valkeyClient,
		option:       option,
		clientConfig: cfg,
		spanName:     namingschema.OpName(namingschema.ValkeyOutbound),
		host:         host,
		port:         port,
	}
	return &client{
		coreClient: core,
	}, nil
}

func splitHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		log.Error("%q cannot be split: %s", addr, err)
		return "", 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

type commander interface {
	Commands() []string
}

func processCmd(commander commander) (command, statement string, size int) {
	commands := commander.Commands()
	if len(commands) == 0 {
		return "", "", 0
	}
	command = commands[0]
	statement = strings.Join(commands, "\n")
	return command, statement, len(statement)
}

func processMultiCmds(multi []commander) (command, statement string, size int) {
	var commands []string
	var statements []string
	for _, cmd := range multi {
		cmdStr, stmt, cmdSize := processCmd(cmd)
		size += cmdSize
		commands = append(commands, cmdStr)
		statements = append(statements, stmt)
	}
	command = strings.Join(commands, " ")
	statement = strings.Join(statements, "\n")
	return command, statement, size
}

func processMultiCompleted(multi ...valkey.Completed) (command, statement string, size int) {
	cmds := make([]commander, len(multi))
	for i, cmd := range multi {
		cmds[i] = &cmd
	}
	return processMultiCmds(cmds)
}

func processMultiCacheableTTL(multi ...valkey.CacheableTTL) (command, statement string, size int) {
	cmds := make([]commander, len(multi))
	for i, cmd := range multi {
		cmds[i] = &cmd.Cmd
	}
	return processMultiCmds(cmds)
}

func firstError(s []valkey.ValkeyResult) error {
	for _, result := range s {
		if err := result.Error(); err != nil && !valkey.IsValkeyNil(err) {
			return err
		}
	}
	return nil
}

func setClientCacheTags(s tracer.Span, result valkey.ValkeyResult) {
	s.SetTag(ext.ValkeyClientCacheHit, result.IsCacheHit())
	s.SetTag(ext.ValkeyClientCacheTTL, result.CacheTTL())
	s.SetTag(ext.ValkeyClientCachePTTL, result.CachePTTL())
	s.SetTag(ext.ValkeyClientCachePXAT, result.CachePXAT())
}

type buildStartSpanOptionsInput struct {
	command        string
	statement      string
	size           int
	skipRawCommand bool
}

func (c *coreClient) peerTags() []tracer.StartSpanOption {
	ipAddr := net.ParseIP(c.host)
	var peerHostKey string
	if ipAddr == nil {
		peerHostKey = ext.PeerHostname
	} else if ipAddr.To4() != nil {
		peerHostKey = ext.PeerHostIPV4
	} else {
		peerHostKey = ext.PeerHostIPV6
	}
	return []tracer.StartSpanOption{
		tracer.Tag(ext.PeerService, ext.DBSystemValkey),
		tracer.Tag(peerHostKey, c.host),
		tracer.Tag(ext.PeerPort, c.port),
	}
}

func (c *coreClient) buildStartSpanOptions(input buildStartSpanOptionsInput) []tracer.StartSpanOption {
	opts := []tracer.StartSpanOption{
		tracer.ResourceName(input.statement),
		tracer.Tag(ext.DBStatement, input.statement),
		tracer.SpanType(ext.SpanTypeValkey),
		tracer.Tag(ext.TargetHost, c.host),
		tracer.Tag(ext.TargetPort, c.port),
		tracer.Tag(ext.ValkeyClientVersion, valkey.LibVer),
		tracer.Tag(ext.ValkeyClientName, valkey.LibName),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemValkey),
		tracer.Tag(ext.ValkeyDatabaseIndex, c.option.SelectDB),
		tracer.Tag("db.out", c.option.SelectDB),
	}
	opts = append(opts, c.peerTags()...)
	if input.command != "" {
		opts = append(opts, []tracer.StartSpanOption{
			// valkeyotel tags
			tracer.Tag("db.stmt_size", input.size),
			tracer.Tag("db.operation", input.command),
		}...)
	}
	if input.skipRawCommand {
		opts = append(opts, tracer.Tag(ext.ValkeyRawCommand, input.skipRawCommand))
	}
	if c.option.Username != "" {
		opts = append(opts, tracer.Tag(ext.DBUser, c.option.Username))
	}
	return opts
}

func (c *coreClient) Do(ctx context.Context, cmd valkey.Completed) (resp valkey.ValkeyResult) {
	command, statement, size := processCmd(&cmd)
	span, ctx := tracer.StartSpanFromContext(ctx, c.spanName, c.buildStartSpanOptions(buildStartSpanOptionsInput{
		command:        command,
		statement:      statement,
		size:           size,
		skipRawCommand: c.clientConfig.skipRaw,
	})...)
	resp = c.Client.Do(ctx, cmd)
	setClientCacheTags(span, resp)
	defer span.Finish(tracer.WithError(resp.Error()))
	return resp
}

func (c *coreClient) DoMulti(ctx context.Context, multi ...valkey.Completed) (resp []valkey.ValkeyResult) {
	command, statement, size := processMultiCompleted(multi...)
	span, ctx := tracer.StartSpanFromContext(ctx, c.spanName, c.buildStartSpanOptions(buildStartSpanOptionsInput{
		command:        command,
		statement:      statement,
		size:           size,
		skipRawCommand: c.clientConfig.skipRaw,
	})...)
	resp = c.Client.DoMulti(ctx, multi...)
	defer span.Finish(tracer.WithError(firstError(resp)))
	return resp
}

func (c *coreClient) Receive(ctx context.Context, subscribe valkey.Completed, fn func(msg valkey.PubSubMessage)) (err error) {
	command, statement, size := processCmd(&subscribe)
	span, ctx := tracer.StartSpanFromContext(ctx, c.spanName, c.buildStartSpanOptions(buildStartSpanOptionsInput{
		command:        command,
		statement:      statement,
		size:           size,
		skipRawCommand: c.clientConfig.skipRaw,
	})...)
	err = c.Client.Receive(ctx, subscribe, fn)
	defer span.Finish(tracer.WithError(err))
	return err
}

func (c *client) DoCache(ctx context.Context, cmd valkey.Cacheable, ttl time.Duration) (resp valkey.ValkeyResult) {
	command, statement, size := processCmd(&cmd)
	span, ctx := tracer.StartSpanFromContext(ctx, c.spanName, c.buildStartSpanOptions(buildStartSpanOptionsInput{
		command:        command,
		statement:      statement,
		size:           size,
		skipRawCommand: c.clientConfig.skipRaw,
	})...)
	resp = c.Client.DoCache(ctx, cmd, ttl)
	setClientCacheTags(span, resp)
	defer span.Finish(tracer.WithError(resp.Error()))
	return resp
}

func (c *client) DoMultiCache(ctx context.Context, multi ...valkey.CacheableTTL) (resp []valkey.ValkeyResult) {
	command, statement, size := processMultiCacheableTTL(multi...)
	span, ctx := tracer.StartSpanFromContext(ctx, c.spanName, c.buildStartSpanOptions(buildStartSpanOptionsInput{
		command:        command,
		statement:      statement,
		size:           size,
		skipRawCommand: c.clientConfig.skipRaw,
	})...)
	resp = c.Client.DoMultiCache(ctx, multi...)
	defer span.Finish(tracer.WithError(firstError(resp)))
	return resp
}

func (c *client) DoStream(ctx context.Context, cmd valkey.Completed) (resp valkey.ValkeyResultStream) {
	command, statement, size := processCmd(&cmd)
	span, ctx := tracer.StartSpanFromContext(ctx, c.spanName, c.buildStartSpanOptions(buildStartSpanOptionsInput{
		command:        command,
		statement:      statement,
		size:           size,
		skipRawCommand: c.clientConfig.skipRaw,
	})...)
	resp = c.Client.DoStream(ctx, cmd)
	defer span.Finish(tracer.WithError(resp.Error()))
	return resp
}

func (c *client) DoMultiStream(ctx context.Context, multi ...valkey.Completed) (resp valkey.MultiValkeyResultStream) {
	command, statement, size := processMultiCompleted(multi...)
	span, ctx := tracer.StartSpanFromContext(ctx, c.spanName, c.buildStartSpanOptions(buildStartSpanOptionsInput{
		command:        command,
		statement:      statement,
		size:           size,
		skipRawCommand: c.clientConfig.skipRaw,
	})...)
	resp = c.Client.DoMultiStream(ctx, multi...)
	defer span.Finish(tracer.WithError(resp.Error()))
	return resp
}

func (c *client) Dedicated(fn func(valkey.DedicatedClient) error) (err error) {
	return c.Client.Dedicated(func(dc valkey.DedicatedClient) error {
		return fn(&dedicatedClient{
			coreClient:      c.coreClient,
			dedicatedClient: dc,
		})
	})
}

func (c *client) Dedicate() (client valkey.DedicatedClient, cancel func()) {
	dedicated, cancel := c.coreClient.Client.Dedicate()
	return &dedicatedClient{
		coreClient:      c.coreClient,
		dedicatedClient: dedicated,
	}, cancel
}

func (c *client) Nodes() map[string]valkey.Client {
	nodes := c.Client.Nodes()
	for addr, valkeyClient := range nodes {
		host, port := splitHostPort(addr)
		nodes[addr] = &client{
			coreClient: coreClient{
				Client:       valkeyClient,
				option:       c.option,
				clientConfig: c.clientConfig,
				host:         host,
				port:         port,
			},
		}
	}
	return nodes
}

func (c *dedicatedClient) SetPubSubHooks(hooks valkey.PubSubHooks) <-chan error {
	return c.dedicatedClient.SetPubSubHooks(hooks)
}
