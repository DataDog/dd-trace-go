// Package redigo provides functions to trace the garyburd/redigo package (https://github.com/garyburd/redigo).
package redigo

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	redis "github.com/garyburd/redigo/redis"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// Conn is an implementation of the redis.Conn interface that supports tracing
type Conn struct {
	redis.Conn
	*params
}

// params contains fields and metadata useful for command tracing
type params struct {
	tracer  *tracer.Tracer
	service string
	network string
	host    string
	port    string
}

// Dial dials into the network address and returns a traced redis.Conn.
func Dial(network, address string, options ...redis.DialOption) (redis.Conn, error) {
	return DialWithServiceName("redis.conn", tracer.DefaultTracer, network, address, options...)
}

// DialWithServiceName dials into the network address using the given options. It augments the returned connection
// with tracing, under the given service name.
//
// TODO(gbbr): Remove tracer argument when we switch to OT.
func DialWithServiceName(service string, tracer *tracer.Tracer, network, address string, options ...redis.DialOption) (redis.Conn, error) {
	c, err := redis.Dial(network, address, options...)
	if err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	tracer.SetServiceInfo(service, "redis", ext.AppTypeDB)
	tc := Conn{c, &params{tracer, service, network, host, port}}
	return tc, nil
}

// DialURL connects to a Redis server at the given URL using the Redis
// URI scheme. URLs should follow the draft IANA specification for the
// scheme (https://www.iana.org/assignments/uri-schemes/prov/redis).
// The returned redis.Conn is traced.
func DialURL(rawurl string, options ...redis.DialOption) (redis.Conn, error) {
	return DialURLWithServiceName("redis.url-conn", tracer.DefaultTracer, rawurl, options...)
}

// DialURLWith name behaves in the same way as DialURL, except it allows specifying the
// service name to be used when tracing the connection.
//
// TODO(gbbr): Remove tracer argument when we switch to OT.
func DialURLWithServiceName(service string, tracer *tracer.Tracer, rawurl string, options ...redis.DialOption) (redis.Conn, error) {
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
	c, err := redis.DialURL(rawurl, options...)
	tc := Conn{c, &params{tracer, service, network, host, port}}
	return tc, err
}

// newChildSpan creates a span inheriting from the given context. It adds to the span useful metadata about the traced Redis connection
func (tc Conn) newChildSpan(ctx context.Context) *tracer.Span {
	p := tc.params
	span := p.tracer.NewChildSpanFromContext("redis.command", ctx)
	span.Service = p.service
	span.SetMeta("out.network", p.network)
	span.SetMeta("out.port", p.port)
	span.SetMeta("out.host", p.host)
	return span
}

// Do wraps redis.Conn.Do. It sends a command to the Redis server and returns the received reply.
// In the process it emits a span containing key information about the command sent.
// When passed a context.Context as the final argument, Do will ensure that any span created
// inherits from this context. The rest of the arguments are passed through to the Redis server unchanged.
func (tc Conn) Do(commandName string, args ...interface{}) (reply interface{}, err error) {
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

	span := tc.newChildSpan(ctx)
	defer func() {
		if err != nil {
			span.SetError(err)
		}
		span.Finish()
	}()

	span.SetMeta("redis.args_length", strconv.Itoa(len(args)))

	if len(commandName) > 0 {
		span.Resource = commandName
	} else {
		// When the command argument to the Do method is "", then the Do method will flush the output buffer
		// See https://godoc.org/github.com/garyburd/redigo/redis#hdr-Pipelining
		span.Resource = "redigo.Conn.Flush"
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
	span.SetMeta("redis.raw_command", b.String())
	return tc.Conn.Do(commandName, args...)
}
