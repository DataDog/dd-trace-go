// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package valkey provides tracing functions for tracing the valkey-io/valkey-go package (https://github.com/valkey-io/valkey-go).
package valkey

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/valkey-io/valkey-go/v2"
	"github.com/valkey-io/valkey-go"
)

// NewClient returns a new valkey.Client enhanced with tracing.
func NewClient(clientOption valkey.ClientOption, opts ...Option) (valkey.Client, error) {
	return v2.NewClient(clientOption, opts...)
}
