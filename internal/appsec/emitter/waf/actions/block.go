// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package actions

import (
	_ "embed" // embed is used to embed the blocked-template.json and blocked-template.html files
	"net/http"
	"os"
	"strings"

	"github.com/mitchellh/mapstructure"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// blockedTemplateJSON is the default JSON template used to write responses for blocked requests
//
//go:embed blocked-template.json
var blockedTemplateJSON []byte

// blockedTemplateHTML is the default HTML template used to write responses for blocked requests
//
//go:embed blocked-template.html
var blockedTemplateHTML []byte

const (
	envBlockedTemplateHTML = "DD_APPSEC_HTTP_BLOCKED_TEMPLATE_HTML"
	envBlockedTemplateJSON = "DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON"
)

func init() {
	for env, template := range map[string]*[]byte{envBlockedTemplateJSON: &blockedTemplateJSON, envBlockedTemplateHTML: &blockedTemplateHTML} {
		if path, ok := os.LookupEnv(env); ok {
			if t, err := os.ReadFile(path); err != nil {
				log.Error("Could not read template at %s: %v", path, err)
			} else {
				*template = t
			}
		}
	}

	registerActionHandler("block_request", NewBlockAction)
}

const (
	BlockingTemplateJSON blockingTemplateType = "json"
	BlockingTemplateHTML blockingTemplateType = "html"
	BlockingTemplateAuto blockingTemplateType = "auto"
)

type (
	blockingTemplateType string

	// blockActionParams are the dynamic parameters to be provided to a "block_request"
	// action type upon invocation
	blockActionParams struct {
		// GRPCStatusCode is the gRPC status code to be returned. Since 0 is the OK status, the value is nullable to
		// be able to distinguish between unset and defaulting to Abort (10), or set to OK (0).
		GRPCStatusCode *int                 `mapstructure:"grpc_status_code,omitempty"`
		StatusCode     int                  `mapstructure:"status_code"`
		Type           blockingTemplateType `mapstructure:"type,omitempty"`
	}
	// GRPCWrapper is an opaque prototype abstraction for a gRPC handler (to avoid importing grpc)
	// that returns a status code and an error
	GRPCWrapper func() (uint32, error)

	// BlockGRPC are actions that interact with a GRPC request flow
	BlockGRPC struct {
		GRPCWrapper
	}

	// BlockHTTP are actions that interact with an HTTP request flow
	BlockHTTP struct {
		http.Handler
	}

	HTTPBlockHandlerConfig struct {
		Template    []byte
		ContentType string
		StatusCode  int
	}
)

func (a *BlockGRPC) EmitData(op dyngo.Operation) {
	dyngo.EmitData(op, a)
	dyngo.EmitData(op, &events.BlockingSecurityEvent{})
}

func (a *BlockHTTP) EmitData(op dyngo.Operation) {
	dyngo.EmitData(op, a)
	dyngo.EmitData(op, &events.BlockingSecurityEvent{})
}

func newGRPCBlockRequestAction(status int) *BlockGRPC {
	return &BlockGRPC{GRPCWrapper: func() (uint32, error) {
		return uint32(status), &events.BlockingSecurityEvent{}
	}}
}

func blockParamsFromMap(params map[string]any) (blockActionParams, error) {
	grpcCode := 10
	parsedParams := blockActionParams{
		Type:           BlockingTemplateAuto,
		StatusCode:     403,
		GRPCStatusCode: &grpcCode,
	}

	if err := mapstructure.WeakDecode(params, &parsedParams); err != nil {
		return parsedParams, err
	}

	if parsedParams.GRPCStatusCode == nil {
		parsedParams.GRPCStatusCode = &grpcCode
	}

	return parsedParams, nil
}

// NewBlockAction creates an action for the "block_request" action type
func NewBlockAction(params map[string]any) []Action {
	p, err := blockParamsFromMap(params)
	if err != nil {
		log.Debug("appsec: couldn't decode redirect action parameters")
		return nil
	}
	return []Action{
		newHTTPBlockRequestAction(p.StatusCode, p.Type),
		newGRPCBlockRequestAction(*p.GRPCStatusCode),
	}
}

func newHTTPBlockRequestAction(statusCode int, template blockingTemplateType) *BlockHTTP {
	return &BlockHTTP{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		template := template
		if template == BlockingTemplateAuto {
			template = blockingTemplateTypeFromHeaders(request.Header)
		}

		if UnwrapGetStatusCode(writer) != 0 {
			// The status code has already been set, so we can't change it, do nothing
			return
		}

		blocker, found := UnwrapBlocker(writer)
		if found {
			// We found our custom response writer, so we can block futur calls to Write and WriteHeader
			defer blocker()
		}

		writer.Header().Set("Content-Type", template.ContentType())
		writer.WriteHeader(statusCode)
		writer.Write(template.Template())
	})}
}

func blockingTemplateTypeFromHeaders(headers http.Header) blockingTemplateType {
	hdr := headers.Get("Accept")
	htmlIdx := strings.Index(hdr, "text/html")
	jsonIdx := strings.Index(hdr, "application/json")
	// Switch to html handler if text/html comes before application/json in the Accept header
	if htmlIdx != -1 && (jsonIdx == -1 || htmlIdx < jsonIdx) {
		return BlockingTemplateHTML
	}

	return BlockingTemplateJSON
}

func (typ blockingTemplateType) Template() []byte {
	if typ == BlockingTemplateHTML {
		return blockedTemplateHTML
	}

	return blockedTemplateJSON
}

func (typ blockingTemplateType) ContentType() string {
	if typ == BlockingTemplateHTML {
		return "text/html"
	}

	return "application/json"
}

// UnwrapBlocker unwraps the right struct method from contrib/internal/httptrace.responseWriter
// and returns the Block() function and if it was found.
func UnwrapBlocker(writer http.ResponseWriter) (func(), bool) {
	// this is part of the contrib/internal/httptrace.responseWriter interface
	wrapped, ok := writer.(interface {
		Block()
	})
	if !ok {
		// Somehow we can't access the wrapped response writer, so we can't block the response
		return nil, false
	}

	return wrapped.Block, true
}

// UnwrapGetStatusCode unwraps the right struct method from contrib/internal/httptrace.responseWriter
// and calls it to know if a call to WriteHeader has been made and returns the status code.
func UnwrapGetStatusCode(writer http.ResponseWriter) int {
	// this is part of the contrib/internal/httptrace.responseWriter interface
	wrapped, ok := writer.(interface {
		Status() int
	})
	if !ok {
		// Somehow we can't access the wrapped response writer, so we can't get the status code
		return 0
	}

	return wrapped.Status()
}
