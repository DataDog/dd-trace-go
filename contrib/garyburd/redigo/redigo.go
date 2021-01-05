// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redigo provides functions to trace the garyburd/redigo package (https://github.com/garyburd/redigo).
package redigo

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	redis "github.com/garyburd/redigo/redis"
)

// Conn is an implementation of the redis.Conn interface that supports tracing
type Conn struct {
	redis.Conn
	*params
}

// params contains fields and metadata useful for command tracing
type params struct {
	config  *dialConfig
	network string
	host    string
	port    string
}

// parseOptions parses a set of arbitrary options (which can be of type redis.DialOption
// or the local DialOption) and returns the corresponding redis.DialOption set as well as
// a configured dialConfig.
func parseOptions(options ...interface{}) ([]redis.DialOption, *dialConfig) {
	dialOpts := []redis.DialOption{}
	cfg := new(dialConfig)
	defaults(cfg)
	for _, opt := range options {
		switch o := opt.(type) {
		case redis.DialOption:
			dialOpts = append(dialOpts, o)
		case DialOption:
			o(cfg)
		}
	}
	return dialOpts, cfg
}

// Dial dials into the network address and returns a traced redis.Conn.
// The set of supported options must be either of type redis.DialOption or this package's DialOption.
func Dial(network, address string, options ...interface{}) (redis.Conn, error) {
	dialOpts, cfg := parseOptions(options...)
	c, err := redis.Dial(network, address, dialOpts...)
	if err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	tc := Conn{c, &params{cfg, network, host, port}}
	return tc, nil
}

// DialURL connects to a Redis server at the given URL using the Redis
// URI scheme. URLs should follow the draft IANA specification for the
// scheme (https://www.iana.org/assignments/uri-schemes/prov/redis).
// The returned redis.Conn is traced.
func DialURL(rawurl string, options ...interface{}) (redis.Conn, error) {
	dialOpts, cfg := parseOptions(options...)
	u, err := url.Parse(rawurl)
	if err != nil {
		return Conn{}, err
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
		port = "6379"
	}
	if host == "" {
		host = "localhost"
	}
	network := "tcp"
	c, err := redis.DialURL(rawurl, dialOpts...)
	tc := Conn{c, &params{cfg, network, host, port}}
	return tc, err
}

// newChildSpan creates a span inheriting from the given context. It adds to the span useful metadata about the traced Redis connection
func (tc Conn) newChildSpan(ctx context.Context) ddtrace.Span {
	p := tc.params
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.ServiceName(p.config.serviceName),
	}
	if !math.IsNaN(p.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(ctx, "redis.command", opts...)
	span.SetTag("out.network", p.network)
	span.SetTag(ext.TargetPort, p.port)
	span.SetTag(ext.TargetHost, p.host)
	return span
}

// Do wraps redis.Conn.Do. It sends a command to the Redis server and returns the received reply.
// In the process it emits a span containing key information about the command sent.
// When passed a context.Context as the final argument, Do will ensure that any span created
// inherits from this context. The rest of the arguments are passed through to the Redis server unchanged.
func (tc Conn) Do(commandName string, args ...interface{}) (reply interface{}, err error) {
	ctx := context.Background()

	if n := len(args); n > 0 {
		if argCtx, ok := args[n-1].(context.Context); ok {
			ctx = argCtx
			args = args[:n-1]
		}
	}

	span := tc.newChildSpan(ctx)
	defer func() {
		span.Finish(tracer.WithError(err))
	}()

	span.SetTag("redis.args_length", strconv.Itoa(len(args)))

	if len(commandName) > 0 {
		span.SetTag(ext.ResourceName, commandName)
	} else {
		// When the command argument to the Do method is "", then the Do method will flush the output buffer
		// See https://godoc.org/github.com/garyburd/redigo/redis#hdr-Pipelining
		span.SetTag(ext.ResourceName, "redigo.Conn.Flush")
	}
	var b bytes.Buffer
	b.WriteString(commandName)
	for _, arg := range args {
		b.WriteString(" ")
		switch arg := arg.(type) {
		case string:
			b.WriteString(arg)
		case int:
			b.WriteString(strconv.Itoa(arg))
		case int32:
			b.WriteString(strconv.FormatInt(int64(arg), 10))
		case int64:
			b.WriteString(strconv.FormatInt(arg, 10))
		case fmt.Stringer:
			b.WriteString(arg.String())
		}
	}
	span.SetTag("redis.raw_command", b.String())
	return tc.Conn.Do(commandName, args...)
}
