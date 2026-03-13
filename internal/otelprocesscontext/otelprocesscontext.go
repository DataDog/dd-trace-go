// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelprocesscontext

//go:generate ./proto/generate.sh

import (
	"google.golang.org/protobuf/proto"
)

// PublishProcessContext marshals pc and publishes it to the process context mapping.
func PublishProcessContext(pc *ProcessContext) error {
	b, _ := proto.Marshal(pc)
	return CreateOtelProcessContextMapping(b)
}
