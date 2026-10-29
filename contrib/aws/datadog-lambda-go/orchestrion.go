// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddlambda

import (
	"context"
	"io"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/wrapper"
)

// WrapRuntimeHandler adds invocation instrumentation to an AWS-configured handler.
// It is used by Orchestrion after AWS has applied the handler's options. AWS retains
// responsibility for decoding requests and encoding responses. Instrumentation
// finishes when AWS closes the response reader after sending the response. Direct
// callers must also close the returned io.Closer. Configuration is read from DD_*
// environment variables.
func WrapRuntimeHandler(handler func(context.Context, []byte) (io.Reader, error)) func(context.Context, []byte) (io.Reader, error) {
	setupAppSec()
	return wrapper.WrapRuntimeHandlerWithListeners(handler, initializeListeners(nil)...)
}
