// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package context

import "context"

// ContextProvider is implemented by any value that carries a context.Context
// accessible via a Context() method. *net/http.Request is the canonical example,
// but any custom request or carrier type with the same method satisfies it.
// Orchestrion uses this interface to find a context source when no context.Context
// parameter is directly in scope at a zap log call site.
type ContextProvider interface {
	Context() context.Context
}
