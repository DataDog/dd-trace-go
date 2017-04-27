package tracedredis_test

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	tracedredis "github.com/DataDog/dd-trace-go/tracer/contrib/redigo"
	"github.com/garyburd/redigo/redis"
)

func Example() {
	tr := tracer.DefaultTracer
	c, _ := tracedredis.TracedDial("my-service-name", tr, "tcp", "127.0.0.1:6379")

	// This call will create a basic span with no parent
	c.Do("SET", 1, "truck")

	ctx := context.Background()
	// This will a create a span that inherits from the span in the context if exists
	c.Do("SET", 2, "cheese", ctx)

}

func ExamplePool() {
	tr := tracer.DefaultTracer
	pool := &redis.Pool{
		MaxIdle:     2,
		MaxActive:   3,
		IdleTimeout: 23,
		Wait:        true,
		Dial: func() (redis.Conn, error) {
			return tracedredis.TracedDial("my-service-name", tr, "tcp", "127.0.0.1:6379")
		},
	}

	pc := pool.Get()

	// Span with no parents created with this call
	pc.Do("SET", " whiskey", " glass")

	// Span with parents if exists in the context
	ctx := context.Background()
	pc.Do("GET", "whiskey", ctx)
}
