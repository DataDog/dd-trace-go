// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redigo_test

import (
	"context"
	"log"
	"time"

	"github.com/gomodule/redigo/redis"

	redigotrace "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/gomodule/redigo"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
)

// To start tracing Redis commands, use the TracedDial function to create a connection,
// passing in a service name of choice.
func Example() {
	c, err := redigotrace.Dial("tcp", "127.0.0.1:6379")
	if err != nil {
		log.Fatal(err)
	}

	// Emit spans per command by using your Redis connection as usual
	c.Do("SET", "vehicle", "truck")

	// Use a context to pass information down the call chain
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

	// When passed a context as the final argument, c.Do will emit a span inheriting from 'parent.request'
	c.Do("SET", "food", "cheese", ctx)
	root.Finish()
}

func Example_tracedConn() {
	c, err := redigotrace.Dial("tcp", "127.0.0.1:6379",
		redigotrace.WithServiceName("my-redis-backend"),
		redis.DialKeepAlive(time.Minute),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Emit spans per command by using your Redis connection as usual
	c.Do("SET", "vehicle", "truck")

	// Use a context to pass information down the call chain
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

	// When passed a context as the final argument, c.Do will emit a span inheriting from 'parent.request'
	c.Do("SET", "food", "cheese", ctx)
	root.Finish()
}

// Alternatively, provide a redis URL to the TracedDialURL function
func Example_dialURL() {
	c, err := redigotrace.DialURL("redis://127.0.0.1:6379")
	if err != nil {
		log.Fatal(err)
	}
	c.Do("SET", "vehicle", "truck")
}

// When using a redigo Pool, set your Dial function to return a traced connection
func Example_pool() {
	pool := &redis.Pool{
		Dial: func() (redis.Conn, error) {
			return redigotrace.Dial("tcp", "127.0.0.1:6379",
				redigotrace.WithServiceName("my-redis-backend"),
			)
		},
	}

	c := pool.Get()
	c.Do("SET", " whiskey", " glass")
}
