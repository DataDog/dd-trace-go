// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package httpdep calls a net/http client shorthand inside a dependency
// module.
package httpdep

import (
	"context"
	"net/http"
)

// Get calls http.Get with a context.Context in scope.
func Get(ctx context.Context, url string) (*http.Response, error) {
	return http.Get(url)
}
