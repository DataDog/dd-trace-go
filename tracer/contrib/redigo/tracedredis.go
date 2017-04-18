package tracedredis

import (
	"bytes"
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	redis "github.com/garyburd/redigo/redis"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type TracedConn struct {
	redis.Conn
	tracer  *tracer.Tracer
	service string
	network string
	host    string
	port    string
}

func TracedDial(service string, tracer *tracer.Tracer, network, address string, options ...redis.DialOption) (TracedConn, error) {
	c, err := redis.Dial(network, address)
	addr := strings.Split(address, ":")
	host := addr[0]
	port := addr[1]
	tracer.SetServiceInfo(service, "redis", ext.AppTypeDB)
	tc := TracedConn{c, tracer, service, network, host, port}
	return tc, err
}

func (tc *TracedConn) SetService(service string) {
	tc.tracer.SetServiceInfo(service, "redis", ext.AppTypeDB)
	tc.service = service
}

func TracedDialURL(service string, tracer *tracer.Tracer, rawurl string, options ...redis.DialOption) (TracedConn, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return TracedConn{}, err
	}

	// Getting host and port, usind code from https://github.com/garyburd/redigo/blob/master/redis/conn.go#L226
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
		port = "6379"
	}
	if host == "" {
		host = "localhost"
	}
	// Set in redis.DialUrl source code
	network := "tcp"
	c, err := redis.DialURL(rawurl, options...)
	tc := TracedConn{c, tracer, service, network, host, port}
	return tc, err
}

func (tc TracedConn) Do(commandName string, args ...interface{}) (reply interface{}, err error) {
	var ctx context.Context
	var ok bool
	if len(args) > 0 {
		ctx, ok = args[len(args)-1].(context.Context)
		if ok {
			args = args[:len(args)-1]
		}
	}
	span := tc.tracer.NewChildSpanFromContext("redis.command", ctx)
	defer span.FinishWithErr(&err)

	span.Service = tc.service
	span.SetMeta("out.network", tc.network)
	span.SetMeta("out.port", tc.port)
	span.SetMeta("out.host", tc.host)
	span.SetMeta("redis.args_length", strconv.Itoa(len(args)))

	if len(commandName) > 0 {
		span.Resource = commandName
	} else {
		// When the command argument to the Do method is "", then the Do method will flush the output buffer
		// check Pipelining in https://godoc.org/github.com/garyburd/redigo/redis
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
		}
	}
	span.SetMeta("redis.raw_command", b.String())
	return tc.Conn.Do(commandName, args...)
}
