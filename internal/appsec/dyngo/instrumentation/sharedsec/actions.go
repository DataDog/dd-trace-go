// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package sharedsec

import (
	_ "embed"
	"net/http"
	"os"
	"strings"

	"google.golang.org/grpc"
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
				log.Warn("Could not read template at %s: %v", path, err)
			} else {
				*template = t
			}
		}

	}
}

type (
	Action struct {
		http       http.Handler
		grpcUnary  grpc.UnaryHandler
		grpcStream grpc.StreamHandler
	}
)

// NewBlockHandler creates, initializes and returns a new BlockRequestAction
func NewBlockHandler(status int, template string) http.Handler {
	htmlHandler := newBlockRequestHandler(status, "text/html", blockedTemplateHTML)
	jsonHandler := newBlockRequestHandler(status, "application/json", blockedTemplateJSON)
	switch template {
	case "json":
		return jsonHandler
	case "html":
		return htmlHandler
	default:
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := jsonHandler
			hdr := r.Header.Get("Accept")
			htmlIdx := strings.Index(hdr, "text/html")
			jsonIdx := strings.Index(hdr, "application/json")
			// Switch to html handler if text/html comes before application/json in the Accept header
			if htmlIdx != -1 && (jsonIdx == -1 || htmlIdx < jsonIdx) {
				h = htmlHandler
			}
			h.ServeHTTP(w, r)
		})
	}
}

func newBlockRequestHandler(status int, ct string, payload []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		w.Write(payload)
	})
}

func NewBlockRequestAction(status int, template string) *Action {
	return &Action{
		http: NewBlockHandler(status, template),
	}
}

func NewRedirectRequestAction(status int, loc string) *Action {
	return &Action{
		http: http.RedirectHandler(loc, status),
	}
}

func (a *Action) HTTP() http.Handler {
	return a.http
}

func (a *Action) GRPCUnary() grpc.UnaryHandler {
	return a.grpcUnary
}

func (a *Action) GRPCStream() grpc.StreamHandler {
	return a.grpcStream
}
