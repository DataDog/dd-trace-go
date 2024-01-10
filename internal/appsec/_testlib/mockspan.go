// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package testlib

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
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
