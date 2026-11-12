// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddlambda

import (
	_ "unsafe" // for go:linkname

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/wrapper"
)

// wrapOrchestrion is called through go:linkname from the Orchestrion aspect
// injected into aws-lambda-go's start function, once per process. It sets up
// ddlambda and returns handler wrapped with the trace, metrics, and payload
// listeners. It stays unexported so it is not part of the public API.
//
//go:linkname wrapOrchestrion
func wrapOrchestrion(handler wrapper.HandlerFunc) wrapper.HandlerFunc {
	setupAppSec()
	return wrapper.WrapHandlerFuncWithListeners(handler, initializeListeners(nil)...)
}
