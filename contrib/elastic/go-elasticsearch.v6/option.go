// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2"
)

// ClientOption represents an option that can be used when creating a client.
type ClientOption = v2.ClientOption

// WithTransport sets the given transport as an http.Transport for the client.
func WithTransport(t http.RoundTripper) ClientOption {
	return v2.WithTransport(t)
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOption {
	return v2.WithService(name)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOption {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) ClientOption {
	return v2.WithAnalyticsRate(rate)
}

// WithResourceNamer specifies a quantizing function which will be used to obtain a resource name for a given
// ElasticSearch request, using the request's URL and method. Note that the default quantizer obfuscates
// IDs and indexes and by replacing it, sensitive data could possibly be exposed, unless the new quantizer
// specifically takes care of that.
func WithResourceNamer(namer func(url, method string) string) ClientOption {
	return v2.WithResourceNamer(namer)
}
