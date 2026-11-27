// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package log stubs internal/log so scan tests run against a self-contained
// fixture module. The stub has the same shape as the real package: Error and
// Warn take a constant format string plus variadic args; Debug and Info exist
// so tests can assert they are not audited.
package log

func Debug(format string, a ...any) {}

func Info(format string, a ...any) {}

func Warn(format string, a ...any) {}

func Error(format string, a ...any) {}
