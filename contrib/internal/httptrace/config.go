// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"os"
	"regexp"
)

const (
	queryStringDisabledEnvVar    = "DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED"
	queryStringObfRegexpEnvVar   = "DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP"
	clientIPHeaderEnvVar         = "DD_TRACE_CLIENT_IP_HEADER"
	clientIPHeaderDisabledEnvVar = "DD_TRACE_CLIENT_IP_HEADER_DISABLED"
	defaultQueryStringObfRegexp  = "(?i)(?:p(?:ass)?w(?:or)?d|pass(?:_?phrase)?|secret|(?:api_?|private_?|public_?|access_?|secret_?)key(?:_?id)?|token|consumer_?(?:id|key|secret)|sign(?:ed|ature)?|auth(?:entication|orization)?)(?:\\s*=[^&]+|\"\\s*:\\s*\"[^\"]+\")|bearer\\s+[a-z0-9\\._\\-]|token:[a-z0-9]{13}|gh[opsu]_[0-9a-zA-Z]{36}|ey[I-L][\\w=-]+\\.ey[I-L][\\w=-]+(?:\\.[\\w.+\\/=-]+)?|[\\-]{5}BEGIN[a-z\\s]+PRIVATE\\sKEY[\\-]{5}[^\\-]+[\\-]{5}END[a-z\\s]+PRIVATE\\sKEY|ssh-rsa\\s*[a-z0-9\\/\\.+]{100,}"
)

type config struct {
	queryStringObfRegexp *regexp.Regexp
	clientIPHeader       string
	collectIP            bool
	collectQueryString   bool
}

func newConfig() config {
	return config{
		clientIPHeader:       os.Getenv(clientIPHeaderEnvVar),
		queryStringObfRegexp: getQueryStringObfRegexp(),
		collectIP:            os.Getenv(clientIPHeaderDisabledEnvVar) != "true",
		collectQueryString:   os.Getenv(queryStringDisabledEnvVar) != "true",
	}
}

// getQueryStringRegexpStr retrieves the regexp string to use to obfuscate the query string from the environment.
// If the env var is not set, the string is defaulted to defaultQueryStringObfRegexp.
// If the env var is set to an empty string, obfuscation is deactivated and nil is returned
func getQueryStringObfRegexp() *regexp.Regexp {
	defaultRegexp := regexp.MustCompile(defaultQueryStringObfRegexp)
	s, set := os.LookupEnv(queryStringObfRegexpEnvVar)
	if !set {
		return defaultRegexp
	}
	if s == "" {
		return nil
	}
	if r, err := regexp.Compile(s); err == nil {
		return r
	}
	return defaultRegexp
}
