package tracedredis

import (
	"bytes"
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/go-redis/redis"
	"strconv"
	"strings"
)

// TracedClient is used to trace requests to a redis server.
type TracedClient struct {
	*redis.Client
	traceParams TraceParams
}

// TracedPipeline is used to trace pipelines with a redis server.
type TracedPipeline struct {
	*redis.Pipeline
	traceParams TraceParams
}

// TraceParams contains the tracer and params that we want to trace.
type TraceParams struct {
	host    string
	port    string
	db      string
	service string
	tracer  *tracer.Tracer
}

// NewTracedClient needs to be called instead of NewClient to trace the calls.
func NewTracedClient(opt *redis.Options, t *tracer.Tracer, service string) *TracedClient {
	var host, port string
	addr := strings.Split(opt.Addr, ":")
	if len(addr) == 2 && addr[1] != "" {
		port = addr[1]
	} else {
		port = "6379"
	}
	host = addr[0]
	db := strconv.Itoa(opt.DB)

	client := redis.NewClient(opt)
	t.SetServiceInfo(service, "redis", ext.AppTypeDB)
	tc := &TracedClient{
		client,
		TraceParams{
			host,
			port,
			db,
			service,
			t},
	}

	tc.Client.WrapProcess(createWrapperFromClient(tc))
	return tc
}

// Pipeline overwrites redis.Pipeline function to create a TracedPipeline.
func (c *TracedClient) Pipeline() *TracedPipeline {
	return &TracedPipeline{
		c.Client.Pipeline(),
		c.traceParams,
	}
}

// ExecWithContext executes traced Exec call with a particular context.
func (c *TracedPipeline) ExecWithContext(ctx context.Context) ([]redis.Cmder, error) {
	span := c.traceParams.tracer.NewChildSpanFromContext("redis.command", ctx)
	span.Service = c.traceParams.service

	span.SetMeta("out.host", c.traceParams.host)
	span.SetMeta("out.port", c.traceParams.port)
	span.SetMeta("out.db", c.traceParams.db)

	cmds, err := c.Pipeline.Exec()
	if err != nil {
		span.SetError(err)
	}
	span.Resource = String(cmds)
	span.SetMeta("redis.pipeline_length", strconv.Itoa(len(cmds)))
	span.Finish()
	return cmds, err
}

// Exec overwrites redis.Exec so that calls made via a TracedClient are traced.
func (c *TracedPipeline) Exec() ([]redis.Cmder, error) {
	span := c.traceParams.tracer.NewRootSpan("redis.command", c.traceParams.service, "redis")

	span.SetMeta("out.host", c.traceParams.host)
	span.SetMeta("out.port", c.traceParams.port)
	span.SetMeta("out.db", c.traceParams.db)

	cmds, err := c.Pipeline.Exec()
	if err != nil {
		span.SetError(err)
	}
	span.Resource = String(cmds)
	span.SetMeta("redis.pipeline_length", strconv.Itoa(len(cmds)))
	span.Finish()
	return cmds, err
}

// String is a used to return a string of all the comands, one comand per line.
func String(cmds []redis.Cmder) string {
	var b bytes.Buffer
	for _, cmd := range cmds {
		b.WriteString(cmd.String())
		b.WriteString("\n")
	}
	return b.String()
}

// SetContext allows to set a context to a TracedClient.
func (c *TracedClient) SetContext(ctx context.Context) {
	c.Client = c.Client.WithContext(ctx)
}

// createWrapperFromClient wraps tracing into redis.Process().
func createWrapperFromClient(tc *TracedClient) func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
	return func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			ctx := tc.Client.Context()

			var resource string
			resource = strings.Split(cmd.String(), " ")[0]
			args_length := len(strings.Split(cmd.String(), " ")) - 1
			span := tc.traceParams.tracer.NewChildSpanFromContext("redis.command", ctx)

			span.Service = tc.traceParams.service
			span.Resource = resource

			span.SetMeta("redis.raw_command", cmd.String())
			span.SetMeta("redis.args_length", strconv.Itoa(args_length))
			span.SetMeta("out.host", tc.traceParams.host)
			span.SetMeta("out.port", tc.traceParams.port)
			span.SetMeta("out.db", tc.traceParams.db)

			err := oldProcess(cmd)
			if err != nil {
				span.SetError(err)
			}
			span.Finish()
			return err
		}
	}
}
