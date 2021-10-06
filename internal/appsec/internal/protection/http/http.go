// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/instrumentation"
)

type EventManager interface {
	SendEvent(event *appsectypes.SecurityEvent)
}

// Register the HTTP protections.
func Register(appsec EventManager) instrumentation.UnregisterFunc {
	return dyngo.Register(waf.NewEventListener(appsec))
}
