// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mapper

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

func NewAppClosingMapper(underlying Mapper) Mapper {
	return &appClosing{wrapper{underlying}}
}

type appClosing struct {
	wrapper
}

func (t *appClosing) Transform(payloads []transport.Payload) ([]transport.Payload, Mapper) {
	return t.wrapper.Transform(append(payloads, transport.AppClosing{}))
}
