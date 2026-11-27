// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddspan

import (
	"context"
	"net/http"
)

//dd:span arg:nil-request
func spanWithNilRequest(_ http.ResponseWriter, r *http.Request) bool {
	return r == nil
}

//dd:span arg:nil-request-context
func spanWithContextAndNilRequest(_ context.Context, r *http.Request) bool {
	return r == nil
}
