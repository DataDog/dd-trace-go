// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// The env vars described below are used to configure the http security tags collection.
// See https://docs.datadoghq.com/tracing/setup_overview/configure_data_security to learn how to use those properly.
const (
	// envQueryStringDisabled is the name of the env var used to disabled query string collection.
	envQueryStringDisabled = "DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED"
	// EnvQueryStringRegexp is the name of the env var used to specify the regexp to use for query string obfuscation.
	EnvQueryStringRegexp = "DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP"
	// envTraceClientIPEnabled is the name of the env var used to specify whether or not to collect client ip in span tags
	envTraceClientIPEnabled = "DD_TRACE_CLIENT_IP_ENABLED"
	// envServerErrorStatuses is the name of the env var used to specify error status codes on http server spans
	envServerErrorStatuses = "DD_TRACE_HTTP_SERVER_ERROR_STATUSES"
	// envInferredProxyServicesEnabled is the name of the env var used for enabling inferred span tracing
	envInferredProxyServicesEnabled = "DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED"
)

// defaultQueryStringRegexp is the regexp used for query string obfuscation if [EnvQueryStringRegexp] is empty.
var defaultQueryStringRegexp = regexp.MustCompile("(?i)(?:p(?:ass)?w(?:or)?d|pass(?:_?phrase)?|secret|(?:api_?|private_?|public_?|access_?|secret_?)key(?:_?id)?|token|consumer_?(?:id|key|secret)|sign(?:ed|ature)?|auth(?:entication|orization)?)(?:(?:\\s|%20)*(?:=|%3D)[^&]+|(?:\"|%22)(?:\\s|%20)*(?::|%3A)(?:\\s|%20)*(?:\"|%22)(?:%2[^2]|%[^2]|[^\"%])+(?:\"|%22))|bearer(?:\\s|%20)+[a-z0-9\\._\\-]|token(?::|%3A)[a-z0-9]{13}|gh[opsu]_[0-9a-zA-Z]{36}|ey[I-L](?:[\\w=-]|%3D)+\\.ey[I-L](?:[\\w=-]|%3D)+(?:\\.(?:[\\w.+\\/=-]|%3D|%2F|%2B)+)?|[\\-]{5}BEGIN(?:[a-z\\s]|%20)+PRIVATE(?:\\s|%20)KEY[\\-]{5}[^\\-]+[\\-]{5}END(?:[a-z\\s]|%20)+PRIVATE(?:\\s|%20)KEY|ssh-rsa(?:\\s|%20)*(?:[a-z0-9\\/\\.+]|%2F|%5C|%2B){100,}")

type config struct {
	queryStringRegexp            *regexp.Regexp // specifies the regexp to use for query string obfuscation.
	queryString                  bool           // reports whether the query string should be included in the URL span tag.
	traceClientIP                bool
	isStatusError                func(statusCode int) bool
	inferredProxyServicesEnabled bool
}

// ResetCfg sets local variable cfg back to its defaults (mainly useful for testing)
func ResetCfg() {
	cfg = newConfig()
}

func newConfig() config {
	c := config{
		queryString:                  !internal.BoolEnv(envQueryStringDisabled, false),
		queryStringRegexp:            QueryStringRegexp(),
		traceClientIP:                internal.BoolEnv(envTraceClientIPEnabled, false),
		isStatusError:                isServerError,
		inferredProxyServicesEnabled: internal.BoolEnv(envInferredProxyServicesEnabled, false),
	}
	v := os.Getenv(envServerErrorStatuses)
	if fn := GetErrorCodesFromInput(v); fn != nil {
		c.isStatusError = fn
	}
	return c
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}

func QueryStringRegexp() *regexp.Regexp {
	if s, ok := os.LookupEnv(EnvQueryStringRegexp); !ok {
		return defaultQueryStringRegexp
	} else if s == "" {
		log.Debug("%s is set but empty. Query string obfuscation will be disabled.", EnvQueryStringRegexp)
		return nil
	} else if r, err := regexp.Compile(s); err == nil {
		return r
	} else {
		log.Error("Could not compile regexp from %s. Using default regexp instead.", EnvQueryStringRegexp)
		return defaultQueryStringRegexp
	}
}

// GetErrorCodesFromInput parses a comma-separated string s to determine which codes are to be considered errors
// Its purpose is to support the DD_TRACE_HTTP_SERVER_ERROR_STATUSES env var
// If error condition cannot be determined from s, `nil` is returned
// e.g, input of "100,200,300-400" returns a function that returns true on 100, 200, and all values between 300-400, inclusive
// any input that cannot be translated to integer values returns nil
func GetErrorCodesFromInput(s string) func(statusCode int) bool {
	if s == "" {
		return nil
	}
	var codes []int
	var ranges [][]int
	vals := strings.Split(s, ",")
	for _, val := range vals {
		// "-" indicates a range of values
		if strings.Contains(val, "-") {
			bounds := strings.Split(val, "-")
			if len(bounds) != 2 {
				log.Debug("Trouble parsing %v due to entry %v, using default error status determination logic", s, val)
				return nil
			}
			before, err := strconv.Atoi(bounds[0])
			if err != nil {
				log.Debug("Trouble parsing %v due to entry %v, using default error status determination logic", s, val)
				return nil
			}
			after, err := strconv.Atoi(bounds[1])
			if err != nil {
				log.Debug("Trouble parsing %v due to entry %v, using default error status determination logic", s, val)
				return nil
			}
			ranges = append(ranges, []int{before, after})
		} else {
			intVal, err := strconv.Atoi(val)
			if err != nil {
				log.Debug("Trouble parsing %v due to entry %v, using default error status determination logic", s, val)
				return nil
			}
			codes = append(codes, intVal)
		}
	}
	return func(statusCode int) bool {
		for _, c := range codes {
			if c == statusCode {
				return true
			}
		}
		for _, bounds := range ranges {
			if statusCode >= bounds[0] && statusCode <= bounds[1] {
				return true
			}
		}
		return false
	}
}
