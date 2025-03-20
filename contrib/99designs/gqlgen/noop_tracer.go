// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gqlgen

import "github.com/99designs/gqlgen/graphql"

var _ graphql.HandlerExtension = &noopHooks{}

type noopHooks struct{}

func (h *noopHooks) ExtensionName() string {
	return "DatadogTracingNoop"
}

func (h *noopHooks) Validate(_ graphql.ExecutableSchema) error {
	return nil
}
