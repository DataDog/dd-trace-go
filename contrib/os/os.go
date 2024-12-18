// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package os

// These imports satisfy injected dependencies for Orchestrion auto instrumentation.
import (
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/ossec"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	_ "gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
)
