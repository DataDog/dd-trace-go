// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import (
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type codeOriginFrame struct {
	file   string
	line   string
	typ    string
	method string
}

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	status        int
	handlerFrames []codeOriginFrame
}

// ResetStatusCode resets the status code of the response writer.
func ResetStatusCode(w http.ResponseWriter) {
	if rw, ok := w.(*responseWriter); ok {
		rw.status = 0
	}
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		status:         0,
	}
}

// Status returns the status code that was monitored.
func (w *responseWriter) Status() int {
	return w.status
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitly call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	w.status = status
	if cfg.codeOriginEnabled {
		w.handlerFrames = extractCodeOriginFrames()
	}
}

// Unwrap returns the underlying wrapped http.ResponseWriter.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func extractCodeOriginFrames() []codeOriginFrame {
	frameN := 0
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs) // skip 2 frames: Callers + this function
	pcs = pcs[:n]

	result := make([]codeOriginFrame, 0)

	frames := runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()

		if isUserCode(frame) {
			co := codeOriginFrame{
				file: frame.File,
				line: strconv.Itoa(frame.Line),
			}

			fn, ok := parseFunction(frame.Function)
			if ok {
				if fn.receiver != "" {
					co.typ = fn.pkg + "." + fn.receiver
					co.method = fn.name
				} else {
					co.method = fn.pkg + "." + fn.name
				}
			} else {
				instr.Logger().Debug("instrumentation/httptrace/extractCodeOriginFrames: failed to extract function info from frame: %s", frame.Function)
			}
			result = append(result, co)

			frameN++
			if frameN >= cfg.codeOriginMaxUserFrames {
				break
			}
		}
		if !more {
			break
		}
	}
	return result
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
