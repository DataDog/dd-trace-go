// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package options

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestStringSliceModify(t *testing.T) {
	t.Run("modify-original", func(t *testing.T) {
		opts := []ddtrace.StartSpanOption{tracer.Tag("mytag", "myvalue")}
		optsCopy := Copy(opts...)
		opts[0] = tracer.ResourceName("somethingelse")
		cfg := new(ddtrace.StartSpanConfig)
		for _, fn := range optsCopy {
			fn(cfg)
		}
		assert.Equal(t, "myvalue", cfg.Tags["mytag"])
	})
	t.Run("modify-copy", func(t *testing.T) {
		opts := []ddtrace.StartSpanOption{tracer.Tag("mytag", "myvalue")}
		optsCopy := Copy(opts...)
		optsCopy[0] = tracer.ResourceName("somethingelse")
		cfg := new(ddtrace.StartSpanConfig)
		for _, fn := range opts {
			fn(cfg)
		}
		assert.Equal(t, "myvalue", cfg.Tags["mytag"])
	})
}
