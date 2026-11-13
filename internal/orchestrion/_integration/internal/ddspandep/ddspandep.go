// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ddspandep has a //dd:span function inside a dependency module.
package ddspandep

import "context"

// Work does nothing. The instrumentation creates its span.
//
//dd:span dep:ddspandep
func Work(ctx context.Context) {}
