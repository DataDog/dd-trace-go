// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build windows

package app

import logger "example.com/fixture/internal/log"

func windowsOnly() {
	logger.Warn("windows-only warning")
}
