// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/internal"
)

// Register global operation event listeners to listen for root operations.
func Register(listeners ...instrumentation.EventListener) instrumentation.UnregisterFunc {
	return internal.RootOperation.Register(listeners...)
}
