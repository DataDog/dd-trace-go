package tracedredis

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-redis/redis"
	"strconv"
	"strings"
)

func NewTracedClient(opt *redis.Options, ctx context.Context, t *tracer.Tracer) *TracedClient {
	var host, port string
	addr := strings.Split(opt.Addr, ":")
	host = addr[0]
	port = addr[1]
	db := strconv.Itoa(opt.DB)
	ctx = context.WithValue(ctx, "_datadog_redis_host", host)
	ctx = context.WithValue(ctx, "_datadog_redis_port", port)
	ctx = context.WithValue(ctx, "_datadog_redis_db", db)
	client := redis.NewClient(opt)
	client.WithContext(ctx)
	client.WrapProcess(createWrapperWithContext(ctx, t))

	return &TracedClient{
		client,
		host,
		port,
		db,
		t,
	}
}

type TracedClient struct {
	*redis.Client
	host   string
	port   string
	db     string
	tracer *tracer.Tracer
}

type TracedPipeline struct {
	*redis.Pipeline
	host   string
	port   string
	db     string
	tracer *tracer.Tracer
}

func (c *TracedClient) TracedPipeline() *TracedPipeline {
	return &TracedPipeline{
		c.Pipeline(),
		c.host,
		c.port,
		c.db,
		c.tracer,
	}
}

func (c *TracedPipeline) TracedExec(ctx context.Context) ([]redis.Cmder, error) {
	t := c.tracer
	span := t.NewChildSpanFromContext("redis.command", ctx)
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

func createWrapperWithContext(ctx context.Context, t *tracer.Tracer) func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
	return func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {

			var resource string
			resource = strings.Split(cmd.String(), " ")[0]
			args_length := len(strings.Split(cmd.String(), " ")) - 1
			span := t.NewChildSpanFromContext("redis.command", ctx)
			span.Resource = resource
			span.SetMeta("redis.raw_command", cmd.String())
			span.SetMeta("redis.args_length", strconv.Itoa(args_length))

			metas := map[string]string{"out.host": "_datadog_redis_host",
				"out.port": "_datadog_redis_port",
				"out.db":   "_datadog_redis_db"}
			for k, v := range metas {
				if val, ok := ctx.Value(v).(string); ok {
					span.SetMeta(k, val)
				}
			}
			err := oldProcess(cmd)
			if err != nil {
				span.SetError(err)
			}
			span.Finish()
			return err
		}
	}
}
