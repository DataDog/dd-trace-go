// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
)

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig struct {
	// Framework is the name of the framework or library being used (optional).
	Framework string
	// Service specifies the service name to use. If left blank, the global service name
	// will be inherited.
	Service string
	// Resource optionally specifies the resource name for this request.
	Resource string
	// QueryParams should be true in order to append the URL query values to the  "http.url" tag.
	QueryParams bool
	// Route is the request matched route if any, if empty, a quantization algorithm will create one using the request URL.
	Route string
	// RouteParams specifies framework-specific route parameters (e.g. for route /user/:id coming
	// in as /user/123 we'll have {"id": "123"}). This field is optional and is used for monitoring
	// by AppSec. It is only taken into account when AppSec is enabled.
	RouteParams map[string]string
	// FinishOpts specifies any options to be used when finishing the request span.
	FinishOpts []tracer.FinishOption
	// SpanOpts specifies any options to be applied to the request starting span.
	SpanOpts []tracer.StartSpanOption
	// isStatusError allows customization of error code determination.
	IsStatusError func(int) bool
	// Handler is the http handler (used to extract information for code origins).
	Handler http.Handler
}

// BeforeHandle contains functionality that should be executed before a http.Handler runs.
// It returns the "traced" http.ResponseWriter and http.Request, an additional afterHandle function
// that should be executed after the Handler runs, and a handled bool that instructs if the request has been handled
// or not - in case it was handled, the original handler should not run.
func BeforeHandle(cfg *ServeConfig, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func(), bool) {
	if cfg == nil {
		cfg = new(ServeConfig)
	}
	opts := options.Expand(cfg.SpanOpts, 2, 3)
	// Pre-append span.kind, component and http.route tags to the options so that they can be overridden.
	opts[0] = tracer.Tag(ext.SpanKind, ext.SpanKindServer)
	opts[1] = tracer.Tag(ext.Component, "net/http")
	if cfg.Service != "" {
		opts = append(opts, tracer.ServiceName(cfg.Service))
	}
	if cfg.Resource != "" {
		opts = append(opts, tracer.ResourceName(cfg.Resource))
	}
	if cfg.Route != "" {
		opts = append(opts, tracer.Tag(ext.HTTPRoute, cfg.Route))
	}
	span, ctx, finishSpans := StartRequestSpan(r, opts...)
	rw, ddrw := wrapResponseWriter(w)
	rt := r.WithContext(ctx)
	closeSpan := func() {
		// TODO: remove this conditional, this is just for testing
		if _, ok := os.LookupEnv("_DD_TEST_CODE_ORIGINS_RUNTIME_CALLERS"); ok {
			setCodeOriginTagsRuntimeCallers(span)
		} else {
			setCodeOriginTags(span, cfg.Handler)
		}
		finishSpans(ddrw.status, cfg.IsStatusError, cfg.FinishOpts...)
	}
	afterHandle := closeSpan
	handled := false
	if appsec.Enabled() {
		route := cfg.Route
		if route == "" {
			var quantizer urlQuantizer
			route = quantizer.Quantize(r.URL.EscapedPath())
		}
		appsecConfig := &httpsec.Config{
			Framework:   cfg.Framework,
			Route:       route,
			RouteParams: cfg.RouteParams,
		}

		secW, secReq, secAfterHandle, secHandled := httpsec.BeforeHandle(rw, rt, span, appsecConfig)
		afterHandle = func() {
			secAfterHandle()
			closeSpan()
		}
		rw = secW
		rt = secReq
		handled = secHandled
	}
	return rw, rt, afterHandle, handled
}

const (
	tagCodeOriginType           = "_dd.code_origin.type"
	tagCodeOriginFrameFile      = "_dd.code_origin.frames.%d.file"
	tagCodeOriginFrameLine      = "_dd.code_origin.frames.%d.line"
	tagCodeOriginFrameType      = "_dd.code_origin.frames.%d.type"
	tagCodeOriginFrameMethod    = "_dd.code_origin.frames.%d.method"
	tagCodeOriginFrameSignature = "_dd.code_origin.frames.%d.signature"
)

func setCodeOriginTags(span *tracer.Span, handler http.Handler) {
	if !cfg.codeOriginEnabled || handler == nil {
		return
	}
	span.SetTag(tagCodeOriginType, "entry")

	file, line, err := getSourceLocation(handler)
	if err != nil {
		instr.Logger().Debug("instrumentation/httptrace/setCodeOriginTags: failed to extract handler information: %v", err)
		return
	}

	span.SetTag(fmt.Sprintf(tagCodeOriginFrameFile, 0), file)
	span.SetTag(fmt.Sprintf(tagCodeOriginFrameLine, 0), strconv.Itoa(line))
}

func getSourceLocation(h http.Handler) (string, int, error) {
	var ptr uintptr

	switch h.(type) {
	case http.HandlerFunc:
		ptr = reflect.ValueOf(h).Pointer()
	default:
		t := reflect.TypeOf(h)
		method, ok := t.MethodByName("ServeHTTP")
		if !ok {
			return "", 0, fmt.Errorf("no ServeHTTP method found")
		}
		ptr = method.Func.Pointer()
	}

	fnInfo := runtime.FuncForPC(ptr)
	if fnInfo == nil {
		return "", 0, errors.New("no function info found")
	}
	file, line := fnInfo.FileLine(ptr)
	return file, line, nil
}

func setCodeOriginTagsRuntimeCallers(span *tracer.Span) {
	if !cfg.codeOriginEnabled {
		return
	}
	span.SetTag(tagCodeOriginType, "entry")

	frameN := 0
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs) // skip 2 frames: Callers + this function
	pcs = pcs[:n]

	frames := runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()
		fmt.Printf("got frame: %s:%d | %s\n", frame.File, frame.Line, frame.Function)

		if isUserCode(frame) {
			span.SetTag(frameTag(tagCodeOriginFrameFile, frameN), frame.File)
			span.SetTag(frameTag(tagCodeOriginFrameLine, frameN), strconv.Itoa(frame.Line))

			fn, ok := parseFunction(frame.Function)
			if ok {
				if fn.receiver != "" {
					span.SetTag(frameTag(tagCodeOriginFrameType, frameN), fn.pkg+"."+fn.receiver)
					span.SetTag(frameTag(tagCodeOriginFrameMethod, frameN), fn.name)
				} else {
					span.SetTag(frameTag(tagCodeOriginFrameMethod, frameN), fn.pkg+"."+fn.name)
				}
			} else {
				instr.Logger().Debug("instrumentation/httptrace/setCodeOriginTags: failed to extract function info from frame: %s", frame.Function)
			}

			frameN++
			if frameN >= cfg.codeOriginMaxUserFrames {
				break
			}
		}
		if !more {
			break
		}
	}
}

func isUserCode(frame runtime.Frame) bool {
	return !isStdLib(frame.File) && !isThirdParty(frame.File)
}

func isStdLib(path string) bool {
	// FIXME: dummy logic, just to test
	return strings.HasPrefix(path, "/opt/homebrew/opt/go")
}

func isThirdParty(path string) bool {
	// FIXME: dummy logic, just to test
	return !(strings.HasSuffix(path, "github.com/DataDog/dd-trace-go/contrib/net/http/code_origin_handlers_test.go") ||
		strings.HasSuffix(path, "github.com/DataDog/dd-trace-go/contrib/net/http/code_origin_test.go"))
}

func frameTag(tag string, n int) string {
	return fmt.Sprintf(tag, n)
}

var funcPattern = regexp.MustCompile(`^(?P<Package>[\w./-]+)(?:\.\((?P<Receiver>[^)]+)\))?\.(?P<Method>\w+)$`)

type funcInfo struct {
	pkg      string
	receiver string
	name     string
}

func parseFunction(fn string) (funcInfo, bool) {
	match := funcPattern.FindStringSubmatch(fn)
	if match == nil {
		return funcInfo{}, false
	}
	return funcInfo{
		pkg:      match[1],
		receiver: match[2],
		name:     match[3],
	}, true
}
