// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package consul

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/hashicorp/consul/v2"

	consul "github.com/hashicorp/consul/api"
)

// Client wraps the regular *consul.Client and augments it with tracing. Use NewClient to initialize it.
type Client = v2.Client

// NewClient returns a traced Consul client.
func NewClient(config *consul.Config, opts ...ClientOption) (*Client, error) {
	return v2.NewClient(config, opts...)
}

// WrapClient wraps a given consul.Client with a tracer under the given service name.
func WrapClient(c *consul.Client, opts ...ClientOption) *Client {
	return v2.WrapClient(c, opts...)
}

// A KV is used to trace requests to Consul's KV.
type KV = v2.KV
