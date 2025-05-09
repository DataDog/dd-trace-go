// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package api provides functions to trace the google.golang.org/api package.
//
// WARNING: Please note we periodically re-generate the endpoint metadata that is used to enrich some tags
// added by this integration using the latest versions of github.com/googleapis/google-api-go-client (which does not
// follow semver due to the auto-generated nature of the package). For this reason, there might be unexpected changes
// in some tag values like service.name and resource.name, depending on the google.golang.org/api that you are using in your
// project. If this is not an acceptable behavior for your use-case, you can disable this feature using the
// WithEndpointMetadataDisabled option.
package api // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/api"

//go:generate go run ./internal/gen_endpoints -o gen_endpoints.json

import (
	_ "embed"
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/google.golang.org/api/v2"
)

// NewClient creates a new oauth http client suitable for use with the google
// APIs with all requests traced automatically.
func NewClient(options ...Option) (*http.Client, error) {
	return v2.NewClient(options...)
}

// WrapRoundTripper wraps a RoundTripper intended for interfacing with
// Google APIs and traces all requests.
func WrapRoundTripper(transport http.RoundTripper, options ...Option) http.RoundTripper {
	return v2.WrapRoundTripper(transport, options...)
}
