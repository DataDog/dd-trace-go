// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"reflect"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

type configuration struct {
	mu     sync.Mutex
	config map[string]transport.ConfKeyValue
	size   int
	seqID  uint64
}

func (c *configuration) Add(key string, value any, origin types.Origin) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config == nil {
		c.config = make(map[string]transport.ConfKeyValue)
	}

	if kv, ok := c.config[key]; ok {
		c.size -= len(kv.Name) + guessSize(kv.Value)
	}

	c.config[key] = transport.ConfKeyValue{
		Name:   key,
		Value:  value,
		Origin: origin,
	}

	c.size += len(key) + guessSize(value)
}

// guessSize returns a simple guess of the value's size in bytes.
// 99% of config values are strings so a simple guess is enough.
// All non-primitive types will go through reflection at encoding time anyway
func guessSize(value any) int {
	switch value.(type) {
	case string:
		return len(value.(string))
	case int64, uint64, int, uint, float64, uintptr:
		return 8
	case int32, uint32, float32:
		return 4
	case int16, uint16:
		return 2
	case bool, int8, uint8:
		return 1
	}

	return int(reflect.ValueOf(value).Type().Size())
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
		delete(c.config, conf.Name)
	}
	c.size = 0
	return transport.AppClientConfigurationChange{
		Configuration: configs,
	}
}

func (c *configuration) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}
