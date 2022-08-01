// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"os"
	"regexp"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// The env vars described below are used to configure the http security tags collection.
// See https://docs.datadoghq.com/tracing/setup_overview/configure_data_security to learn how to use those properly.
const (
	// envQueryStringDisabled is the name of the env var used to disabled query string collection.
	envQueryStringDisabled = "DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED"
	// envQueryStringRegexp is the name of the env var used to specify the regexp to use for query string obfuscation.
	envQueryStringRegexp = "DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP"
	// envClientIPHeader is the name of the env var used to specify the IP header to be used for client IP collection.
	envClientIPHeader = "DD_TRACE_CLIENT_IP_HEADER"
	// envClientIPHeader is the name of the env var used to disable client IP tag collection.
	envClientIPHeaderDisabled = "DD_TRACE_CLIENT_IP_HEADER_DISABLED"
)

// defaultQueryStringRegexp is the regexp used for query string obfuscation if `envQueryStringRegexp` is empty.
var defaultQueryStringRegexp = regexp.MustCompile("(?i)(?:p(?:ass)?w(?:or)?d|pass(?:_?phrase)?|secret|(?:api_?|private_?|public_?|access_?|secret_?)key(?:_?id)?|token|consumer_?(?:id|key|secret)|sign(?:ed|ature)?|auth(?:entication|orization)?)(?:(?:\\s|%20)*(?:=|%3D)[^&]+|(?:\"|%22)(?:\\s|%20)*(?::|%3A)(?:\\s|%20)*(?:\"|%22)(?:%2[^2]|%[^2]|[^\"%])+(?:\"|%22))|bearer(?:\\s|%20)+[a-z0-9\\._\\-]|token(?::|%3A)[a-z0-9]{13}|gh[opsu]_[0-9a-zA-Z]{36}|ey[I-L](?:[\\w=-]|%3D)+\\.ey[I-L](?:[\\w=-]|%3D)+(?:\\.(?:[\\w.+\\/=-]|%3D|%2F|%2B)+)?|[\\-]{5}BEGIN(?:[a-z\\s]|%20)+PRIVATE(?:\\s|%20)KEY[\\-]{5}[^\\-]+[\\-]{5}END(?:[a-z\\s]|%20)+PRIVATE(?:\\s|%20)KEY|ssh-rsa(?:\\s|%20)*(?:[a-z0-9\\/\\.+]|%2F|%5C|%2B){100,}")

type config struct {
	queryStringRegexp *regexp.Regexp // specifies the regexp to use for query string obfuscation.
	clientIPHeader    string         // specifies the header to use for IP extraction if client IP tag collection is enabled.
	clientIP          bool           // reports whether the IP should be extracted from the request headers and added to span tags.
	queryString       bool           // reports whether the query string should be included in the URL span tag.
}

func newConfig() config {
	c := config{
		clientIPHeader:    os.Getenv(envClientIPHeader),
		clientIP:          !internal.BoolEnv(envClientIPHeaderDisabled, false),
		queryString:       !internal.BoolEnv(envQueryStringDisabled, false),
		queryStringRegexp: defaultQueryStringRegexp,
	}
	if s, ok := os.LookupEnv(envQueryStringRegexp); !ok {
		return c
	} else if s == "" {
		c.queryStringRegexp = nil
		log.Debug("%s is set but empty. Query string obfuscation will be disabled.", envQueryStringRegexp)
	} else if r, err := regexp.Compile(s); err == nil {
		c.queryStringRegexp = r
	} else {
		log.Debug("Could not compile regexp from %s. Using default regexp instead.", envQueryStringRegexp)
	}
	return c
}
