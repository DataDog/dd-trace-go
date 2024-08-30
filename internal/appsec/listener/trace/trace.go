// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package trace

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

// TODO: move all internal/appsec/trace code in this package

func NewSpanTransport(_ *config.Config, rootOp dyngo.Operation) (func(), error) {
	dyngo.On(rootOp, OnServiceEntryStart)
	dyngo.On(rootOp, OnSpanStart)
	return func() {}, nil
}
