// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package memcache provides functions to trace the bradfitz/gomemcache package (https://github.com/bradfitz/gomemcache).
//
// `WrapClient` will wrap a memcache `Client` and return a new struct with all
// the same methods, so should be seamless for existing applications. It also
// has an additional `WithContext` method which can be used to connect a span
// to an existing trace.
package memcache // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/bradfitz/gomemcache/memcache"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/bradfitz/gomemcache/v2/memcache"

	"github.com/bradfitz/gomemcache/memcache"
)

// WrapClient wraps a memcache.Client so that all requests are traced using the
// default tracer with the service name "memcached".
func WrapClient(client *memcache.Client, opts ...ClientOption) *Client {
	return v2.WrapClient(client, opts...)
}

// A Client is used to trace requests to the memcached server.
type Client = v2.Client
