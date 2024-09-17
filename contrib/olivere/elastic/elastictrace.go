// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package elastic provides functions to trace the gopkg.in/olivere/elastic.v{3,5} packages.
package elastic // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/olivere/elastic"

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/olivere/elastic.v5/v2"
)

// NewHTTPClient returns a new http.Client which traces requests under the given service name.
func NewHTTPClient(opts ...ClientOption) *http.Client {
	return v2.NewHTTPClient(opts...)
}

// bodyCutoff specifies the maximum number of bytes that will be stored as a tag
// value obtained from an HTTP request or response body.
var bodyCutoff = 5 * 1024
