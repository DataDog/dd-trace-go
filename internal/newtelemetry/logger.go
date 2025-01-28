// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

type LogOption func(key *loggerKey, value *loggerValue)

// WithTags returns a LogOption that sets the tags for the telemetry log message. Tags are key-value pairs that are then
// serialized into a simple "key:value,key2:value2" format. No quoting or escaping is performed.
func WithTags(tags map[string]string) LogOption {
	compiledTags := ""
	for k, v := range tags {
		compiledTags += k + ":" + v + ","
	}
	compiledTags = strings.TrimSuffix(compiledTags, ",")
	return func(key *loggerKey, _ *loggerValue) {
		if key == nil {
			return
		}
		key.tags = compiledTags
	}
}

// WithStacktrace returns a LogOption that sets the stacktrace for the telemetry log message. The stacktrace is a string
// that is generated inside the WithStacktrace function. Logs demultiplication does not take the stacktrace into account.
// This means that a log that has been demultiplicated will only show of the first log.
func WithStacktrace() LogOption {
	buf := make([]byte, 1<<12)
	buf = buf[:runtime.Stack(buf, false)]
	return func(_ *loggerKey, value *loggerValue) {
		if value == nil {
			return
		}
		value.stacktrace = string(buf)
	}
}

type LogLevel = transport.LogLevel

const (
	LogDebug = transport.LogLevelDebug
	LogWarn  = transport.LogLevelWarn
	LogError = transport.LogLevelError
)

type loggerKey struct {
	tags    string
	message string
	level   LogLevel
}

type loggerValue struct {
	count      atomic.Uint32
	stacktrace string
	time       int64 // Unix timestamp
}

type logger struct {
	store atomic.Pointer[internal.TypedSyncMap[loggerKey, *loggerValue]]
}

func (logger *logger) Add(level LogLevel, text string, opts ...LogOption) {
	store := logger.store.Load()
	if store == nil {
		store = new(internal.TypedSyncMap[loggerKey, *loggerValue])
		for logger.store.CompareAndSwap(nil, store) {
			continue
		}
	}

	key := loggerKey{
		message: text,
		level:   level,
	}

	for _, opt := range opts {
		opt(&key, nil)
	}

	val, ok := store.Load(key)
	if ok {
		val.count.Add(1)
		return
	}

	newVal := &loggerValue{
		time: time.Now().Unix(),
	}

	for _, opt := range opts {
		opt(&key, newVal)
	}

	newVal.count.Store(1)
	store.Store(key, newVal)
}

func (logger *logger) Payload() transport.Payload {
	store := logger.store.Swap(nil)
	if store == nil {
		return nil
	}

	var logs []transport.LogMessage
	store.Range(func(key loggerKey, value *loggerValue) bool {
		logs = append(logs, transport.LogMessage{
			Message:    key.message,
			Level:      key.level,
			Tags:       key.tags,
			Count:      value.count.Load(),
			StackTrace: value.stacktrace,
			TracerTime: value.time,
		})
		return true
	})

	store.Clear()
	return transport.Logs{Logs: logs}
}
