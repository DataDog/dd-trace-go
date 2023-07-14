// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package testlib

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

type MockSpan struct {
	Tags     map[string]interface{}
	Finished bool
}

func (m *MockSpan) SetTag(key string, value interface{}) {
	if m.Tags == nil {
		m.Tags = make(map[string]interface{})
	}
	if key == ext.ManualKeep {
		if value == samplernames.AppSec {
			m.Tags[ext.ManualKeep] = true
		}
	} else {
		m.Tags[key] = value
	}
}

func (m *MockSpan) SetOperationName(_ string) {
	panic("unused")
}

func (m *MockSpan) BaggageItem(_ string) string {
	panic("unused")
}

func (m *MockSpan) SetBaggageItem(_, _ string) {
	panic("unused")
}

func (m *MockSpan) Finish(_ ...ddtrace.FinishOption) {
	m.Finished = true
}

func (m *MockSpan) Context() ddtrace.SpanContext {
	panic("unused")
}
