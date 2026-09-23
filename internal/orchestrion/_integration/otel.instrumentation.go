// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build tools

// Enables otelc instrumentation for this module, the otelc counterpart of
// orchestrion.tool.go.
package tools

import (
	_ "github.com/DataDog/dd-trace-go/otelc/all/v2" // integration
)
