// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelprocesscontext

import (
	"fmt"

	processcontextv1 "go.opentelemetry.io/proto/otlp/processcontext/v1development"
	"google.golang.org/protobuf/proto"
)

// PublishProcessContext marshals pc and publishes it to the process context mapping.
func PublishProcessContext(pc *processcontextv1.ProcessContext) error {
	b, err := proto.Marshal(pc)
	if err != nil {
		return fmt.Errorf("failed to marshal process context: %w", err)
	}
	return CreateOtelProcessContextMapping(b)
}
