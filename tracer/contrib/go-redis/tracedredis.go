package tracedredis

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-redis/redis"
	"strconv"
	"strings"
)

func NewTracedClient(opt *redis.Options, t *tracer.Tracer, service string) *TracedClient {
	var host, port string
	addr := strings.Split(opt.Addr, ":")
	host = addr[0]
	port = addr[1]
	db := strconv.Itoa(opt.DB)

	client := redis.NewClient(opt)

	tc := &TracedClient{
		client,
		host,
		port,
		db,
		service,
		t,
	}

	tc.Client.WrapProcess(createWrapperFromClient(tc))
	return tc
}

type TracedClient struct {
	*redis.Client
	host    string
	port    string
	db      string
	service string
	tracer  *tracer.Tracer
}

func (c *TracedClient) SetContext(ctx context.Context) {
	c.Client = c.Client.WithContext(ctx)
}

type TracedPipeline struct {
	*redis.Pipeline
	host    string
	port    string
	db      string
	service string
	tracer  *tracer.Tracer
}

func (c *TracedClient) Pipeline() *TracedPipeline {
	return &TracedPipeline{
		c.Client.Pipeline(),
		c.host,
		c.port,
		c.db,
		c.service,
		c.tracer,
	}
}

func (c *TracedPipeline) TracedExec(ctx context.Context) ([]redis.Cmder, error) {
	span := c.tracer.NewChildSpanFromContext("redis.command", ctx)
	span.Service = c.service

	span.SetMeta("out.host", c.host)
	span.SetMeta("out.port", c.port)
	span.SetMeta("out.db", c.db)

	cmds, err := c.Exec()
	if err != nil {
		span.SetError(err)
	}
	span.SetMeta("redis.pipeline_length", strconv.Itoa(len(cmds)))
	span.Finish()
	return cmds, err

}

func createWrapperFromClient(tc *TracedClient) func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
	return func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			ctx := tc.Client.Context()

			var resource string
			resource = strings.Split(cmd.String(), " ")[0]
			args_length := len(strings.Split(cmd.String(), " ")) - 1
			span := tc.tracer.NewChildSpanFromContext("redis.command", ctx)

			span.Service = tc.service
			span.Resource = resource

			span.SetMeta("redis.raw_command", cmd.String())
			span.SetMeta("redis.args_length", strconv.Itoa(args_length))
			span.SetMeta("out.host", tc.host)
			span.SetMeta("out.port", tc.port)
			span.SetMeta("out.db", tc.db)

			err := oldProcess(cmd)
			if err != nil {
				span.SetError(err)
			}
			span.Finish()
			return err
		}
	}
}
