// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the go-redis client constructors from package main,
// which the harness suites never build. TestClientsInMain builds and runs it.
package main

import (
	"fmt"
	"net"

	"github.com/go-redis/redis"

	redistrace "github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2"
)

func main() {
	// Nothing listens on addr, so each command fails fast.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	get(redis.NewClient(&redis.Options{Addr: addr}), "main-new-client")
	get(redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    "master",
		SentinelAddrs: []string{addr},
	}), "main-new-failover-client")
	contribClient := redistrace.NewClient(&redis.Options{Addr: addr})
	_ = contribClient.Get("main-contrib-new-client").Err()
	_ = contribClient.Close()
	getAliased(addr)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func get(client *redis.Client, key string) {
	_ = client.Get(key).Err()
	_ = client.Close()
}
