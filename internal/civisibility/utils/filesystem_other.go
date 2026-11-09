//go:build !unix && !windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

// differentFilesystems cannot tell filesystems apart here; callers keep their
// first choice.
func differentFilesystems(_, _ string) bool { return false }
