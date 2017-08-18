// Package redis provides tracing for the go-redis Redis client (https://github.com/go-redis/redis)
package redis

import (
	"bytes"
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/go-redis/redis"
	"strconv"
	"strings"
)

// Client is used to trace requests to a redis server.
type Client struct {
	*redis.Client
	*traceParams
}

type traceParams struct {
	host    string
	port    string
	db      string
	service string
	tracer  *tracer.Tracer
}

// NewClient takes a Client returned by redis.NewClient and configures it to emit spans under the given service name
// The last parameter is optional and allows to pass a custom tracer.
func NewClient(opt *redis.Options, service string, trc ...*tracer.Tracer) *Client {
	var port string

	t := getTracer(trc)
	t.SetServiceInfo(service, "redis", ext.AppTypeDB)

	addr := strings.Split(opt.Addr, ":")
	if len(addr) == 2 && addr[1] != "" {
		port = addr[1]
	} else {
		port = "6379"
	}
	host := addr[0]
	db := strconv.Itoa(opt.DB)

	client := redis.NewClient(opt)
	c := &Client{
		client,
		&traceParams{
			host,
			port,
			db,
			service,
			t,
		},
	}

	c.WrapProcess(createWrapperFromClient(c))
	return c
}

// SetContext sets a context on a Client. Use it to ensure that emitted spans have the correct parent
func (c *Client) SetContext(ctx context.Context) {
	c.Client = c.Client.WithContext(ctx)
}

// Pipeline allocates and returns a Pipeline from a Client
func (c *Client) Pipeline() *Pipeline {
	return &Pipeline{
		c.Client.Pipeline(),
		c.traceParams,
	}
}

// Pipeline is used to trace pipelines executed on a redis server.
type Pipeline struct {
	redis.Pipeliner
	*traceParams
}

// ExecContext calls Pipeline.Exec(). It ensures that the resulting Redis calls
// are traced, and that emitted spans are children of the given Context
func (p *Pipeline) ExecContext(ctx context.Context) ([]redis.Cmder, error) {
	span := p.tracer.NewChildSpanFromContext("redis.command", ctx)
	if span == nil {
		p.tracer.NewRootSpan("redis.command", p.service, "redis")
	}
	span.Service = p.service

	span.SetMeta("out.host", p.host)
	span.SetMeta("out.port", p.port)
	span.SetMeta("out.db", p.db)

	cmds, err := p.Pipeliner.Exec()
	if err != nil {
		span.SetError(err)
	}
	span.Resource = String(cmds)
	span.SetMeta("redis.pipeline_length", strconv.Itoa(len(cmds)))
	span.Finish()
	return cmds, err
}

// Exec calls Pipeline.ExecContext() with a non-nil empty context
func (p *Pipeline) Exec() ([]redis.Cmder, error) {
	return p.ExecContext(context.Background())
}

// createWrapperFromClient wraps tracing into redis.Process().
func createWrapperFromClient(c *Client) func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
	return func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			ctx := c.Client.Context()

			var resource string
			resource = strings.Split(cmd.String(), " ")[0]
			args_length := len(strings.Split(cmd.String(), " ")) - 1
			span := c.tracer.NewChildSpanFromContext("redis.command", ctx)

			span.Service = c.service
			span.Resource = resource

			span.SetMeta("redis.raw_command", cmd.String())
			span.SetMeta("redis.args_length", strconv.Itoa(args_length))
			span.SetMeta("out.host", c.host)
			span.SetMeta("out.port", c.port)
			span.SetMeta("out.db", c.db)

			err := oldProcess(cmd)
			if err != nil {
				span.SetError(err)
			}
			span.Finish()
			return err
		}
	}
}

// getTracer returns either the tracer passed as the last argument or a default tracer.
func getTracer(tracers []*tracer.Tracer) *tracer.Tracer {
	var t *tracer.Tracer
	if len(tracers) == 0 || (len(tracers) > 0 && tracers[0] == nil) {
		t = tracer.DefaultTracer
	} else {
		t = tracers[0]
	}
	return t
}

// String returns a string representation of a slice of redis Commands, separated by newlines
func String(cmds []redis.Cmder) string {
	var b bytes.Buffer
	for _, cmd := range cmds {
		b.WriteString(cmd.String())
		b.WriteString("\n")
	}
	return b.String()
}
