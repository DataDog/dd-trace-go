// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import "github.com/DataDog/dd-trace-go/v2/ddtrace"

// SetPropagatingTag is an intermediary holder to push the propagating tag code
// into the internal package, so that it can be used by v1internal.
var SetPropagatingTag func(ctx ddtrace.SpanContext, k, v string)
