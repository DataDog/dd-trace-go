// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package zerologdep builds zerolog loggers inside a dependency module.
package zerologdep

import (
	"io"

	"github.com/rs/zerolog"
)

// NewLogger returns a logger built with zerolog.New.
func NewLogger(w io.Writer) zerolog.Logger {
	return zerolog.New(w)
}
