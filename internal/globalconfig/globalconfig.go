// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package globalconfig stores configuration which applies globally to both the tracer
// and integrations.
package globalconfig

import (
	_ "unsafe" // required by go:linkname

	_ "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer" // revive:disable-line:blank-imports
)

//go:linkname runtimeID github.com/DataDog/dd-trace-go/v2/internal/globalconfig.RuntimeID
func runtimeID() string

func RuntimeID() string {
	return runtimeID()
}
