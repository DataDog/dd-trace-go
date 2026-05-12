// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	"github.com/labstack/echo/v5"
)

// envServerErrorStatuses is the name of the env var used to specify error status codes on http server spans
const envServerErrorStatuses = "DD_TRACE_HTTP_SERVER_ERROR_STATUSES"

type config struct {
	serviceName       string
	serviceSource     string
	analyticsRate     float64
	noDebugStack      bool
	ignoreRequestFunc IgnoreRequestFunc
	isStatusError     func(statusCode int) bool
	translateError    func(err error) (*echo.HTTPError, bool)
	headerTags        instrumentation.HeaderTags
	errCheck          func(error) bool
	tags              map[string]any
	// echoInstance, when set (via Wrap), gives the middleware access to the
	// configured [echo.Echo.HTTPErrorHandler] so AppSec error paths can honor
	// custom error renderers instead of writing a hardcoded JSON body. It is
	// also used to lazily install the AppSec Binder wrap on the first request
	// (see Wrap for details).
	echoInstance *echo.Echo
	// bindOnce guards lazy installation of the AppSec Binder wrap.
	bindOnce sync.Once
}

// String renders a debug-friendly view of the user-visible configuration,
// hiding internal plumbing (echoInstance, bindOnce) and turning function
// fields into a simple "set"/"unset" indicator. This is what appears in
// Debug logs that format the config via %v / %s.
func (c *config) String() string {
	if c == nil {
		return "<nil>"
	}
	setOrUnset := func(isSet bool) string {
		if isSet {
			return "set"
		}
		return "unset"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "{serviceName:%q serviceSource:%q analyticsRate:%v noDebugStack:%v",
		c.serviceName, c.serviceSource, c.analyticsRate, c.noDebugStack)
	fmt.Fprintf(&sb, " ignoreRequestFunc:%s isStatusError:%s translateError:%s errCheck:%s",
		setOrUnset(c.ignoreRequestFunc != nil),
		setOrUnset(c.isStatusError != nil),
		setOrUnset(c.translateError != nil),
		setOrUnset(c.errCheck != nil))
	sb.WriteString(" headerTags:")
	formatHeaderTags(&sb, c.headerTags)
	fmt.Fprintf(&sb, " tags:%v}", c.tags)
	return sb.String()
}

// formatHeaderTags renders a [instrumentation.HeaderTags] as a map-style
// "header→tag" listing for debug logs, avoiding the noisy internal struct
// representation that the default %v formatter would produce.
func formatHeaderTags(sb *strings.Builder, ht instrumentation.HeaderTags) {
	if ht == nil {
		sb.WriteString("nil")
		return
	}
	sb.WriteByte('{')
	first := true
	ht.Iter(func(header, tag string) {
		if !first {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(sb, "%s→%s", header, tag)
		first = false
	})
	sb.WriteByte('}')
}

// Option describes options for the Echo.v5 integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to Middleware.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

// IgnoreRequestFunc determines if tracing will be skipped for a request.
type IgnoreRequestFunc func(c *echo.Context) bool

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.serviceSource = string(instrumentation.PackageLabstackEchoV5)
	cfg.analyticsRate = math.NaN()
	if fn := httptrace.GetErrorCodesFromInput(env.Get(envServerErrorStatuses)); fn != nil {
		cfg.isStatusError = fn
	} else {
		cfg.isStatusError = isServerError
	}
	cfg.headerTags = instr.HTTPHeadersAsTags()
	cfg.tags = make(map[string]any)
	cfg.translateError = func(err error) (*echo.HTTPError, bool) {
		var echoErr *echo.HTTPError
		if errors.As(err, &echoErr) {
			return echoErr, true
		}
		return nil, false
	}
}

// WithService sets the given service name for the system.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() OptionFn {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// WithIgnoreRequest sets a function which determines if tracing will be
// skipped for a given request.
func WithIgnoreRequest(ignoreRequestFunc IgnoreRequestFunc) OptionFn {
	return func(cfg *config) {
		cfg.ignoreRequestFunc = ignoreRequestFunc
	}
}

// WithErrorTranslator sets a function to translate Go errors into echo Errors.
// This is used for extracting the HTTP response status code.
func WithErrorTranslator(fn func(err error) (*echo.HTTPError, bool)) OptionFn {
	return func(cfg *config) {
		cfg.translateError = fn
	}
}

// WithStatusCheck specifies a function fn which reports whether the passed
// statusCode should be considered an error.
func WithStatusCheck(fn func(statusCode int) bool) OptionFn {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) OptionFn {
	headerTagsMap := instrumentation.NewHeaderTags(headers)
	return func(cfg *config) {
		cfg.headerTags = headerTagsMap
	}
}

// WithErrorCheck sets the func which determines if err would be ignored (if it returns true, the error is not tagged).
// This function also checks the errors created from the WithStatusCheck option.
func WithErrorCheck(errCheck func(error) bool) OptionFn {
	return func(cfg *config) {
		cfg.errCheck = errCheck
	}
}

// WithCustomTag will attach the value to the span tagged by the key. Standard
// span tags cannot be replaced.
func WithCustomTag(key string, value any) OptionFn {
	return func(cfg *config) {
		// The nil check is redundant in practice because defaults() always
		// initializes cfg.tags. It is kept here for parity with the echo.v4
		// integration and as defensive coding against future refactors of
		// defaults() that might drop the eager allocation.
		if cfg.tags == nil {
			cfg.tags = make(map[string]any)
		}
		cfg.tags[key] = value
	}
}

// withEchoInstance is an internal option used by [Wrap] to give the middleware
// access to the configured [echo.Echo] so AppSec error paths can dispatch to
// [echo.Echo.HTTPErrorHandler].
func withEchoInstance(e *echo.Echo) OptionFn {
	return func(cfg *config) {
		cfg.echoInstance = e
	}
}
