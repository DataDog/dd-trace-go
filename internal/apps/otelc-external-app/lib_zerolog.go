// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"io"

	"github.com/rs/zerolog"
)

func init() {
	registerLibrary("zerolog", func() {
		_ = zerolog.New(io.Discard)
	})
}
