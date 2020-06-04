// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package redigo provides functions to trace the gomodule/redigo package (https://github.com/gomodule/redigo).
package redigo

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	redis "github.com/gomodule/redigo/redis"
)

// Conn is an implementation of the redis.Conn interface that supports tracing
type Conn struct {
	redis.Conn
	*params
}

// ConnWithTimeout is an implementation of the redis.ConnWithTimeout interface that supports tracing
type ConnWithTimeout struct {
	redis.ConnWithTimeout
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

func wrapConn(c redis.Conn, p *params) redis.Conn {
	if connWithTimeout, ok := c.(redis.ConnWithTimeout); ok {
		return ConnWithTimeout{connWithTimeout, p}
	}

	return Conn{c, p}
}

// Dial dials into the network address and returns a traced redis.Conn.
// The set of supported options must be either of type redis.DialOption or this package's DialOption.
func Dial(network, address string, options ...interface{}) (redis.Conn, error) {
	dialOpts, cfg := parseOptions(options...)
	log.Debug("contrib/gomodule/redigo: Dialing %s %s, %#v", network, address, cfg)
	c, err := redis.Dial(network, address, dialOpts...)
	if err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	tc := wrapConn(c, &params{cfg, network, host, port})
	return tc, nil
}

// DialURL connects to a Redis server at the given URL using the Redis
// URI scheme. URLs should follow the draft IANA specification for the
// scheme (https://www.iana.org/assignments/uri-schemes/prov/redis).
// The returned redis.Conn is traced.
func DialURL(rawurl string, options ...interface{}) (redis.Conn, error) {
	dialOpts, cfg := parseOptions(options...)
	og.Debug("contrib/gomodule/redigo: Dialing %s, %#v", rawurl, cfg)
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
	tc := wrapConn(c, &params{cfg, network, host, port})
	return tc, err
}

// newChildSpan creates a span inheriting from the given context. It adds to the span useful metadata about the traced Redis connection
func newChildSpan(ctx context.Context, p *params) ddtrace.Span {
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

func withSpan(do func(commandName string, args ...interface{}) (interface{}, error), p *params, commandName string, args ...interface{}) (reply interface{}, err error) {
	var (
		ctx context.Context
		ok  bool
	)
	if n := len(args); n > 0 {
		ctx, ok = args[n-1].(context.Context)
		if ok {
			args = args[:n-1]
		}
	}

	span := newChildSpan(ctx, p)
	defer func() {
		span.Finish(tracer.WithError(err))
	}()

	span.SetTag("redis.args_length", strconv.Itoa(len(args)))

	if len(commandName) > 0 {
		span.SetTag(ext.ResourceName, commandName)
	} else {
		// When the command argument to the Do method is "", then the Do method will flush the output buffer
		// See https://godoc.org/github.com/gomodule/redigo/redis#hdr-Pipelining
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
	return do(commandName, args...)
}

// Do wraps redis.Conn.Do. It sends a command to the Redis server and returns the received reply.
// In the process it emits a span containing key information about the command sent.
// When passed a context.Context as the final argument, Do will ensure that any span created
// inherits from this context. The rest of the arguments are passed through to the Redis server unchanged.
func (tc Conn) Do(commandName string, args ...interface{}) (reply interface{}, err error) {
	return withSpan(
		tc.Conn.Do,
		tc.params,
		commandName,
		args...,
	)
}

// Do wraps redis.Conn.Do. It sends a command to the Redis server and returns the received reply.
// In the process it emits a span containing key information about the command sent.
// When passed a context.Context as the final argument, Do will ensure that any span created
// inherits from this context. The rest of the arguments are passed through to the Redis server unchanged.
func (tc ConnWithTimeout) Do(commandName string, args ...interface{}) (reply interface{}, err error) {
	return withSpan(
		tc.ConnWithTimeout.Do,
		tc.params,
		commandName,
		args...,
	)
}

// DoWithTimeout wraps redis.Conn.DoWithTimeout. It sends a command to the Redis server and returns the received reply.
// In the process it emits a span containing key information about the command sent.
// When passed a context.Context as the final argument, Do will ensure that any span created
// inherits from this context. The rest of the arguments are passed through to the Redis server unchanged.
func (tc ConnWithTimeout) DoWithTimeout(readTimeout time.Duration, commandName string, args ...interface{}) (reply interface{}, err error) {
	return withSpan(
		func(commandName string, args ...interface{}) (interface{}, error) {
			return tc.ConnWithTimeout.DoWithTimeout(readTimeout, commandName, args...)
		},
		tc.params,
		commandName,
		args...,
	)
}
