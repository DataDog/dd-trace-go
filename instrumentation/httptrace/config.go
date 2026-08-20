// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	appsecconfig "github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
	// envPubsubPropagationAsSpanLinks determines if pubsub context is propogated by span link rather than by reparenting
	envPubsubPropagationAsSpanLinks = "DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS"
	// envQueryStringAllowlist is the name of the env var used to specify which query string parameter keys
	// to keep in the http.url span tag. When set, only these keys are retained and the expensive default
	// obfuscation regex is bypassed. Comma-separated list of parameter names.
	envQueryStringAllowlist = "DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST"
	// envClientQueryStringAllowlist overrides envQueryStringAllowlist for HTTP client spans only.
	envClientQueryStringAllowlist = "DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT"
	// envServerQueryStringAllowlist overrides envQueryStringAllowlist for HTTP server spans only.
	envServerQueryStringAllowlist = "DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER"
)

// defaultQueryStringPattern is the regexp used for query string obfuscation if
// [EnvQueryStringRegexp] is not set. Capture group 1 is the delimiter before a
// JWT. A match is replaced by [defaultQueryStringReplacement], which keeps that
// delimiter. All other groups are non-capturing, thus group 1 is empty for the
// matches of the other alternatives.
//
// The tracer does not use this regexp to obfuscate: it uses
// obfuscateQueryStringDefault, a state machine that gives the same result in
// linear time. Keep the two in sync.
const defaultQueryStringPattern = `(?i)(?:(?:"|%22)?)(?:(?:old[-_]?|new[-_]?)?p(?:ass)?w(?:or)?d(?:1|2)?|pass(?:[-_]?phrase)?|secret|(?:api[-_]?|private[-_]?|public[-_]?|access[-_]?|secret[-_]?|app(?:lication)?[-_]?)key(?:[-_]?id)?|token|consumer[-_]?(?:id|key|secret)|sign(?:ed|ature)?|auth(?:entication|orization)?)(?:(?:\s|%20)*(?:=|%3D)[^&]+|(?:"|%22)(?:\s|%20)*(?::|%3A)(?:\s|%20)*(?:"|%22)(?:%2[^2]|%[^2]|[^"%])+(?:"|%22))|(?:bearer(?:\s|%20)+[a-z0-9._\-]+|token(?::|%3A)[a-z0-9]{13}|gh[opsu]_[0-9a-zA-Z]{36}|(^|[^\w%-]|%[0-9a-f]{2})ey[I-L][\w-]+(?:=|%3D)*\.ey[I-L][\w-]+(?:=|%3D)*(?:\.(?:[\w.+/=-]|%3D|%2F|%2B)+)?|-{5}BEGIN(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY-{5}[^\-]+-{5}END(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY(?:-{5})?(?:\n|%0A)?|(?:ssh-(?:rsa|dss)|ecdsa-[a-z0-9]+-[a-z0-9]+)(?:\s|%20|%09)+(?:[a-z0-9/.+]|%2F|%5C|%2B){100,}(?:=|%3D)*(?:(?:\s|%20|%09)+[a-z0-9._-]+)?)`

// defaultQueryStringReplacement is the replacement template for the matches of
// [defaultQueryStringPattern]. It keeps the JWT delimiter (group 1).
const defaultQueryStringReplacement = "${1}<redacted>"

// defaultQueryStringPatternNonCapturing is [defaultQueryStringPattern] with a
// non-capturing JWT delimiter group. With a "replace the whole match"
// algorithm, it gives the same result as [defaultQueryStringPattern]. When
// [EnvQueryStringRegexp] has this value, the tracer also uses the state
// machine.
var defaultQueryStringPatternNonCapturing = strings.Replace(defaultQueryStringPattern, `|(^|[^\w%-]|`, `|(?:^|[^\w%-]|`, 1)

// defaultQueryStringRegexp is the compiled form of [defaultQueryStringPattern].
var defaultQueryStringRegexp = regexp.MustCompile(defaultQueryStringPattern)

// isDefaultQueryStringPattern reports whether s is one of the forms of the
// default query string obfuscation regexp.
func isDefaultQueryStringPattern(s string) bool {
	return s == defaultQueryStringPattern || s == defaultQueryStringPatternNonCapturing
}

type config struct {
	queryStringRegexp                        *regexp.Regexp      // specifies the regexp to use for query string obfuscation.
	useDefaultObfuscator                     bool                // reports whether to use the default query string obfuscator.
	replaceJWTDelimiter                      bool                // reports whether the default obfuscator also replaces the JWT delimiter (the default regexp is set explicitly).
	dropQueryString                          bool                // reports whether the query string must not be reported, because the configured regexp is not valid.
	queryString                              bool                // reports whether the query string should be included in the URL span tag.
	clientQueryStringAllowlist               map[string]struct{} // when non-nil, only keep these query parameter keys for client spans and skip regex obfuscation.
	serverQueryStringAllowlist               map[string]struct{} // when non-nil, only keep these query parameter keys for server spans and skip regex obfuscation.
	traceClientIP                            bool
	otelSemanticsEnabled                     bool
	isStatusError                            func(statusCode int) bool
	inferredProxyServicesEnabled             bool
	pubsubPropagationAsSpanLinks             bool
	allowAllBaggage                          bool                // tag all baggage items when true (DD_TRACE_BAGGAGE_TAG_KEYS="*").
	baggageTagKeys                           map[string]struct{} // when allowAllBaggage is false, only tag baggage items whose keys are listed here.
	resourceRenamingEnabled                  *bool
	resourceRenamingAlwaysSimplifiedEndpoint bool
	appsecEnabledMode                        func() bool // first AppSec enablement mode at startup.
}

func (c config) String() string {
	return fmt.Sprintf("config{queryString: %t, traceClientIP: %t, otelSemanticsEnabled: %t, inferredProxyServicesEnabled: %t}", c.queryString, c.traceClientIP, c.otelSemanticsEnabled, c.inferredProxyServicesEnabled)
}

// ResetCfg sets local variable cfg back to its defaults (mainly useful for testing)
func ResetCfg() {
	cfg = newConfig()
}

func traceClientIPEnabled() bool { return cfg.traceClientIP }

func newConfig() config {
	c := config{
		queryString:                              !internal.BoolEnv(envQueryStringDisabled, false),
		traceClientIP:                            internal.BoolEnv(envTraceClientIPEnabled, false),
		otelSemanticsEnabled:                     instr.OTelSemanticsEnabled(),
		isStatusError:                            isServerError,
		inferredProxyServicesEnabled:             internal.BoolEnv(envInferredProxyServicesEnabled, false),
		pubsubPropagationAsSpanLinks:             internal.BoolEnv(envPubsubPropagationAsSpanLinks, false),
		baggageTagKeys:                           make(map[string]struct{}),
		resourceRenamingAlwaysSimplifiedEndpoint: internal.BoolEnv("DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT", false),
		appsecEnabledMode:                        sync.OnceValue(appsecEnabledAtStartup),
	}
	if s, ok := env.Lookup(EnvQueryStringRegexp); !ok {
		// Use the in-code state-machine obfuscator instead of `defaultQueryStringRegexp`:
		// it gives the same result in linear time.
		c.useDefaultObfuscator = true
	} else if isDefaultQueryStringPattern(s) {
		// A configured regexp has its matches replaced in full, thus the JWT
		// delimiter is also replaced. The state machine gives the same result
		// as the regexp package, in linear time.
		c.useDefaultObfuscator = true
		c.replaceJWTDelimiter = true
	} else if s != "" {
		// An empty value disables the obfuscation.
		if r, err := regexp.Compile(s); err == nil {
			c.queryStringRegexp = r
		} else {
			// Fail closed: do not report a query string that we cannot obfuscate.
			// Do not log the error: it contains the regexp, which can contain
			// sensitive data.
			log.Warn("Could not compile regexp from %s. The query string will not be reported.", EnvQueryStringRegexp)
			c.dropQueryString = true
		}
	}
	if v, ok := env.Lookup("DD_TRACE_BAGGAGE_TAG_KEYS"); ok {
		if v == "*" {
			c.allowAllBaggage = true
		} else {
			for part := range strings.SplitSeq(v, ",") {
				key := strings.TrimSpace(part)
				if key == "" {
					continue
				}
				c.baggageTagKeys[key] = struct{}{}
			}
		}
	} else {
		c.baggageTagKeys = defaultBaggageTagKeys()
	}
	v := env.Get(envServerErrorStatuses)
	if fn := GetErrorCodesFromInput(v); fn != nil {
		c.isStatusError = fn
	}
	if vv, ok := internal.BoolEnvNoDefault("DD_TRACE_RESOURCE_RENAMING_ENABLED"); ok {
		c.resourceRenamingEnabled = &vv
	}
	// Global allowlist applies to both client and server; specific env vars override it.
	if v, ok := env.Lookup(envQueryStringAllowlist); ok && v != "" {
		globalAllowlist := parseAllowlist(v)
		c.clientQueryStringAllowlist = globalAllowlist
		c.serverQueryStringAllowlist = globalAllowlist
	}
	if v, ok := env.Lookup(envClientQueryStringAllowlist); ok && v != "" {
		c.clientQueryStringAllowlist = parseAllowlist(v)
	}
	if v, ok := env.Lookup(envServerQueryStringAllowlist); ok && v != "" {
		c.serverQueryStringAllowlist = parseAllowlist(v)
	}
	return c
}

func appsecEnabledAtStartup() bool {
	enabled, set, _ := appsecconfig.IsEnabledByEnvironment()
	if set {
		return enabled
	}
	return appsec.Enabled()
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}

// QueryStringRegexp returns the regexp configured with [EnvQueryStringRegexp].
// It returns nil when the value is empty, and the default regexp when the value
// is not set or is not a valid regexp.
//
// The tracer does not use this function: it uses a linear-time state machine
// for the default regexp, and it does not report the query string when the
// configured regexp is not valid. When the value is not set or is not valid,
// the returned regexp has a capture group: replace its matches with
// "${1}<redacted>" to keep the JWT delimiter, as the tracer does.
func QueryStringRegexp() *regexp.Regexp {
	s, ok := env.Lookup(EnvQueryStringRegexp)
	if !ok {
		// The value is not set: use the default regexp, with no log.
		return defaultQueryStringRegexp
	}
	if s == "" {
		return nil
	}
	if r, err := regexp.Compile(s); err == nil {
		return r
	}
	log.Error("Could not compile regexp from %s. Using default regexp instead.", EnvQueryStringRegexp)
	return defaultQueryStringRegexp
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
	vals := strings.SplitSeq(s, ",")
	for val := range vals {
		// "-" indicates a range of values
		if strings.Contains(val, "-") {
			bounds := strings.Split(val, "-")
			if len(bounds) != 2 {
				log.Debug("Trouble parsing %q due to entry %q, using default error status determination logic", s, val)
				return nil
			}
			before, err := strconv.Atoi(bounds[0])
			if err != nil {
				log.Debug("Trouble parsing %q due to entry %q, using default error status determination logic", s, val)
				return nil
			}
			after, err := strconv.Atoi(bounds[1])
			if err != nil {
				log.Debug("Trouble parsing %q due to entry %q, using default error status determination logic", s, val)
				return nil
			}
			ranges = append(ranges, []int{before, after})
		} else {
			intVal, err := strconv.Atoi(val)
			if err != nil {
				log.Debug("Trouble parsing %q due to entry %q, using default error status determination logic", s, val)
				return nil
			}
			codes = append(codes, intVal)
		}
	}
	return func(statusCode int) bool {
		if slices.Contains(codes, statusCode) {
			return true
		}
		for _, bounds := range ranges {
			if statusCode >= bounds[0] && statusCode <= bounds[1] {
				return true
			}
		}
		return false
	}
}

func defaultBaggageTagKeys() map[string]struct{} {
	return map[string]struct{}{
		"user.id":    {},
		"account.id": {},
		"session.id": {},
	}
}

// getQueryStringAllowlist returns the allowlist for the given side (client or server).
func (c *config) getQueryStringAllowlist(isClient bool) map[string]struct{} {
	if isClient {
		return c.clientQueryStringAllowlist
	}
	return c.serverQueryStringAllowlist
}

// parseAllowlist parses a comma-separated string into a map of allowed keys.
func parseAllowlist(v string) map[string]struct{} {
	m := make(map[string]struct{})
	for part := range strings.SplitSeq(v, ",") {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		m[key] = struct{}{}
	}
	return m
}

// tagBaggageKey returns true if we should tag this baggage key.
func (c *config) tagBaggageKey(key string) bool {
	if c.allowAllBaggage {
		return true
	}
	_, ok := c.baggageTagKeys[key]
	return ok
}
