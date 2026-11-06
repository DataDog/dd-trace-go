// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than zerolog, which the rules must handle.
import (
	"os"

	zl "github.com/rs/zerolog"
)

func aliasedLogger() zl.Logger {
	return zl.New(os.Stdout)
}
