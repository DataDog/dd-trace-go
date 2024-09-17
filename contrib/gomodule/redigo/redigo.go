// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redigo provides functions to trace the gomodule/redigo package (https://github.com/gomodule/redigo).
package redigo

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2"

	redis "github.com/gomodule/redigo/redis"
)

// Conn is an implementation of the redis.Conn interface that supports tracing
type Conn = v2.Conn

// ConnWithTimeout is an implementation of the redis.ConnWithTimeout interface that supports tracing
type ConnWithTimeout = v2.ConnWithTimeout

// ConnWithContext is an implementation of the redis.ConnWithContext interface that supports tracing
type ConnWithContext = v2.ConnWithContext

// Dial dials into the network address and returns a traced redis.Conn.
// The set of supported options must be either of type redis.DialOption or this package's DialOption.
func Dial(network, address string, options ...interface{}) (redis.Conn, error) {
	return v2.Dial(network, address, options...)
}

// DialContext dials into the network address using redis.DialContext and returns a traced redis.Conn.
// The set of supported options must be either of type redis.DialOption or this package's DialOption.
func DialContext(ctx context.Context, network, address string, options ...interface{}) (redis.Conn, error) {
	return v2.DialContext(ctx, network, address, options...)
}

// DialURL connects to a Redis server at the given URL using the Redis
// URI scheme. URLs should follow the draft IANA specification for the
// scheme (https://www.iana.org/assignments/uri-schemes/prov/redis).
// The returned redis.Conn is traced.
func DialURL(rawurl string, options ...interface{}) (redis.Conn, error) {
	return v2.DialURL(rawurl, options...)
}
