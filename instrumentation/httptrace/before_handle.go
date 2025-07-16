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
		if _, ok := os.LookupEnv("_DD_TEST_CODE_ORIGINS_STACK_TRACE"); ok {
			setCodeOriginTags(span, ddrw.handlerFrames)
		} else {
			setCodeOriginTagsFromReflection(span, cfg.Handler)
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
	tagCodeOriginType = "_dd.code_origin.type"
	// tagCodeOriginFrameFile contains the file where the handler is.
	tagCodeOriginFrameFile = "_dd.code_origin.frames.%d.file"
	// tagCodeOriginFrameLine should contain the first line of executable code within the method that handles the incoming request.
	tagCodeOriginFrameLine = "_dd.code_origin.frames.%d.line"
	// tagCodeOriginFrameType is the fully qualified class/type name.
	tagCodeOriginFrameType = "_dd.code_origin.frames.%d.type"
	// tagCodeOriginFrameMethod contains the method/function name.
	tagCodeOriginFrameMethod = "_dd.code_origin.frames.%d.method"
	// tagCodeOriginFrameSignature contains the signature of the method (used to disambiguate in cases the method has multiple overloads).
	tagCodeOriginFrameSignature = "_dd.code_origin.frames.%d.signature"
)

func setCodeOriginTagsFromReflection(span *tracer.Span, handler http.Handler) {
	if !cfg.codeOriginEnabled || handler == nil {
		return
	}
	fr, err := getSourceLocation(handler)
	if err != nil {
		instr.Logger().Debug("instrumentation/httptrace/setCodeOriginTags: failed to extract handler information: %v", err)
		return
	}
	setCodeOriginTags(span, []codeOriginFrame{fr})
}

func getSourceLocation(h http.Handler) (codeOriginFrame, error) {
	var ptr uintptr

	switch h.(type) {
	case http.HandlerFunc:
		ptr = reflect.ValueOf(h).Pointer()
	default:
		t := reflect.TypeOf(h)
		method, ok := t.MethodByName("ServeHTTP")
		if !ok {
			return codeOriginFrame{}, fmt.Errorf("no ServeHTTP method found")
		}
		ptr = method.Func.Pointer()
	}

	fnInfo := runtime.FuncForPC(ptr)
	if fnInfo == nil {
		return codeOriginFrame{}, errors.New("no function info found")
	}

	result := codeOriginFrame{
		file:   "",
		line:   "",
		typ:    "",
		method: "",
	}
	file, line := fnInfo.FileLine(ptr)
	result.file = file
	result.line = strconv.Itoa(line)

	pkgPath, typeName, methodName := parseFuncName(fnInfo.Name())
	if pkgPath != "" && methodName != "" {
		if typeName != "" {
			result.typ = pkgPath + "." + typeName
			result.method = methodName
		} else {
			result.method = pkgPath + "." + methodName
		}
	}

	return result, nil
}

var funcNameRE = regexp.MustCompile(`^(?P<pkg>.+?)(?:\.(?P<type>\(\*?\w+\)|\w+))?\.(?P<name>\w+)$`)

func parseFuncName(full string) (pkgPath, typeName, methodName string) {
	match := funcNameRE.FindStringSubmatch(full)
	if match == nil {
		return "", "", ""
	}

	groupNames := funcNameRE.SubexpNames()
	for i, name := range groupNames {
		switch name {
		case "pkg":
			pkgPath = match[i]
		case "type":
			typeName = match[i]
		case "name":
			methodName = match[i]
		}
	}

	if typeName != "" {
		typeName = strings.Trim(typeName, "()")
	}
	return
}

func setCodeOriginTags(span *tracer.Span, frames []codeOriginFrame) {
	if !cfg.codeOriginEnabled || len(frames) == 0 {
		return
	}
	tagsSet := false
	for i, fr := range frames {
		if ok := setCodeOriginFrameTags(span, fr, i); ok {
			tagsSet = true
		}
	}
	if tagsSet {
		span.SetTag(tagCodeOriginType, "entry")
	}
}

func setCodeOriginFrameTags(span *tracer.Span, fr codeOriginFrame, idx int) bool {
	if fr.file == "" || fr.line == "" {
		return false
	}
	span.SetTag(frameTag(tagCodeOriginFrameFile, idx), fr.file)
	span.SetTag(frameTag(tagCodeOriginFrameLine, idx), fr.line)
	if fr.typ != "" {
		span.SetTag(frameTag(tagCodeOriginFrameType, idx), fr.typ)
	}
	if fr.method != "" {
		span.SetTag(frameTag(tagCodeOriginFrameMethod, idx), fr.method)
	}
	return true
}
