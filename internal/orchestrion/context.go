// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import "context"

// CtxGLS returns the context stored in the compilation-injected field of the runtime.g struct by orchestrion
func CtxGLS() context.Context {
	return getDDGLS().(context.Context)
}

// CtxOrGLS implements the logic to choose between a user given context.Context or the context.Context inserted in
// the Goroutine Global Storage (GLS) by orchestrion. If orchestrion is not enabled it's only returning the given context.
func CtxOrGLS(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}

	if !Enabled() {
		return context.Background()
	}

	if gls := getDDGLS(); gls != nil {
		return gls.(context.Context)
	}

	return context.Background()
}

// CtxWithValue runs context.WithValue, adds the result to the GLS slot of orchestrion, and returns it.
func CtxWithValue(ctx context.Context, key, val any) context.Context {
	ctx = CtxOrGLS(ctx)
	ctx = context.WithValue(ctx, key, val)

	if Enabled() {
		setDDGLS(ctx)
	}

	return ctx
}
