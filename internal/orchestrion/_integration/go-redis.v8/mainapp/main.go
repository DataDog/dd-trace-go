// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the go-redis client constructors from package main,
// which the harness suites never build. TestClientsInMain builds and runs it.
package main

import (
	"context"
	"fmt"
	"net"

	"github.com/go-redis/redis/v8"

	redistrace "github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2"
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
	get(redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{addr}, MaxRedirects: -1}), "main-new-cluster-client")
	get(redis.NewFailoverClusterClient(&redis.FailoverOptions{
		MasterName:    "master",
		SentinelAddrs: []string{addr},
	}), "main-new-failover-cluster-client")
	get(redis.NewRing(&redis.RingOptions{Addrs: map[string]string{"shard": addr}}), "main-new-ring")
	get(redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{addr}}), "main-new-universal-client")
	get(redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{addr, addr}, MaxRedirects: -1}), "main-new-universal-cluster-client")
	get(redistrace.NewClient(&redis.Options{Addr: addr}), "main-contrib-new-client")
	getAliased(addr)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func get(client redis.UniversalClient, key string) {
	_ = client.Do(context.Background(), "get", key).Err()
	_ = client.Close()
}
