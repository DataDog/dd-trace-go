// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

type configuration struct {
	mu     sync.Mutex
	config map[string]transport.ConfKeyValue
	seqID  uint64
}

func (c *configuration) Add(key string, value any, origin types.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config == nil {
		c.config = make(map[string]transport.ConfKeyValue)
	}

	c.config[key] = transport.ConfKeyValue{
		Name:   key,
		Value:  value,
		Origin: origin,
	}
}

func (c *configuration) Payload() transport.Payload {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.config) == 0 {
		return nil
	}

	configs := make([]transport.ConfKeyValue, len(c.config))
	idx := 0
	for _, conf := range c.config {
		conf.SeqID = c.seqID
		configs[idx] = conf
		idx++
		c.seqID++
	}
	c.config = nil
	return transport.AppClientConfigurationChange{
		Configuration: configs,
	}
}
