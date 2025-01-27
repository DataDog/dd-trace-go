// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

type LogOption func(key *loggerKey)

func WithTags(tags map[string]string) LogOption {
	compiledTags := ""
	for k, v := range tags {
		compiledTags += k + ":" + v + ","
	}
	compiledTags = strings.TrimSuffix(compiledTags, ",")
	return func(key *loggerKey) {
		key.tags = compiledTags
	}
}

func WithStacktrace() LogOption {
	buf := make([]byte, 1<<12)
	runtime.Stack(buf, false)
	return func(key *loggerKey) {
		key.stacktrace = string(buf)
	}
}

type LogLevel = transport.LogLevel

const (
	LogDebug = transport.LogLevelDebug
	LogWarn  = transport.LogLevelWarn
	LogError = transport.LogLevelError
)

type loggerKey struct {
	tags       string
	message    string
	level      LogLevel
	stacktrace string
}

type loggerValue struct {
	count atomic.Uint32
	time  int64 // Unix timestamp
}

type logger struct {
	store atomic.Pointer[sync.Map]
}

func (logger *logger) Add(level LogLevel, text string, opts ...LogOption) {
	store := logger.store.Load()
	if store == nil {
		store = new(sync.Map)
		for logger.store.CompareAndSwap(nil, store) {
			continue
		}
	}

	key := loggerKey{
		message: text,
		level:   level,
	}

	for _, opt := range opts {
		opt(&key)
	}

	val, ok := store.Load(key)
	if ok {
		val.(*loggerValue).count.Add(1)
		return
	}

	newVal := &loggerValue{
		time: time.Now().Unix(),
	}

	newVal.count.Add(1)
	store.Store(key, newVal)
}

func (logger *logger) Payload() transport.Payload {
	store := logger.store.Swap(nil)
	if store == nil {
		return nil
	}

	var logs []transport.LogMessage
	store.Range(func(key, value any) bool {
		k := key.(loggerKey)
		v := value.(*loggerValue)
		logs = append(logs, transport.LogMessage{
			Message:    k.message,
			Level:      k.level,
			Tags:       k.tags,
			Count:      v.count.Load(),
			StackTrace: k.stacktrace,
			TracerTime: v.time,
		})
		return true
	})

	store.Clear()
	return transport.Logs{Logs: logs}
}
