// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package elastic provides functions to trace the github.com/elastic/go-elasticsearch packages.
package elastic // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/elastic/go-elasticsearch

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2"
)

// NewRoundTripper returns a new http.Client which traces requests under the given service name.
func NewRoundTripper(opts ...ClientOption) http.RoundTripper {
	return v2.NewRoundTripper(opts...)
}

// bodyCutoff specifies the maximum number of bytes that will be stored as a tag
// value obtained from an HTTP request or response body.
var bodyCutoff = 5 * 1024
