package tracedredis

import (
	"context"
	"fmt"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-redis/redis"
	"strconv"
	"strings"
)

func NewTracedClient(opt *redis.Options, ctx context.Context, t *tracer.Tracer) *redis.Client {
	var host, port string
	addr := strings.Split(opt.Addr, ":")
	host = addr[0]
	port = addr[1]
	db := strconv.Itoa(opt.DB)
	ctx = context.WithValue(ctx, "_datadog_host", host)
	ctx = context.WithValue(ctx, "_datadog_port", port)
	ctx = context.WithValue(ctx, "_datadog_db", db)
	client := redis.NewClient(opt)
	client.WithContext(ctx)
	client.WrapProcess(createWrapperWithContext(ctx, t))

	return client

}

func createWrapperWithContext(ctx context.Context, t *tracer.Tracer) func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
	return func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {

			var resource string
			resource = strings.Split(cmd.String(), " ")[0]
			span := t.NewChildSpanFromContext("redis.command", ctx)
			span.Resource = resource
			span.SetMeta("redis.raw_command", cmd.String())

			host_str, ok_host := ctx.Value("_datadog_host").(string)
			port_str, ok_port := ctx.Value("_datadog_port").(string)
			db_str, ok_db := ctx.Value("_datadog_db").(string)

			if ok_host {
				span.SetMeta("host", host_str)
			}
			if ok_port {
				span.SetMeta("port", port_str)
			}
			if ok_db {
				span.SetMeta("db", db_str)
			}
			err := oldProcess(cmd)
			if err != nil {
				span.SetError(err)
			}
			fmt.Printf("%s", span.String())
			span.Finish()
			return err
		}
	}
}

func ExampleNewClient() *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	return client
	// Output: PONG <nil>
}

func main() {
	opt := &redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	}
	ctx := context.Background()
	t := tracer.NewTracer()
	span := t.NewChildSpanFromContext("first_span", ctx)
	span.SetMeta("MetaMeta", "22")

	ctx = tracer.ContextWithSpan(ctx, span)

	client := NewTracedClient(opt, ctx, t)

	client.Set("test", 3, 0)
	result, _ := client.Get("test").Result()
	fmt.Printf("%s", span.String())
	s2, _ := tracer.SpanFromContext(ctx)
	fmt.Printf(s2.String())
	span.Finish()
	fmt.Printf(result)
}
