// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

const (
	// EnvClientQueryStringEnabled is the name of the env var used to specify whether query string collection is enabled for http client spans.
	EnvClientQueryStringEnabled = "DD_TRACE_HTTP_CLIENT_TAG_QUERY_STRING"
	// EnvClientErrorStatuses is the name of the env var that specifies error status codes on http client spans
	EnvClientErrorStatuses = "DD_TRACE_HTTP_CLIENT_ERROR_STATUSES"
	// EnvQueryStringRegexp is the name of the env var used to specify the regexp to use for query string obfuscation.
	EnvQueryStringRegexp = "DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP"
)
