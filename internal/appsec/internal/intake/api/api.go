// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package api

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Intake API payloads.
type (
	// EventBatch intake API payload.
	EventBatch struct {
		IdempotencyKey string         `json:"idempotency_key"`
		Events         []*AttackEvent `json:"events"`
	}

	// AttackEvent intake API payload.
	AttackEvent struct {
		EventVersion string           `json:"event_version"`
		EventID      string           `json:"event_id"`
		EventType    string           `json:"event_type"`
		DetectedAt   time.Time        `json:"detected_at"`
		Type         string           `json:"type"`
		Rule         AttackRule       `json:"rule"`
		RuleMatch    *AttackRuleMatch `json:"rule_match"`
		Context      AttackContext    `json:"context"`
	}

	// AttackRule intake API payload.
	AttackRule struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	// AttackRuleMatch intake API payload.
	AttackRuleMatch struct {
		Operator      string                     `json:"operator"`
		OperatorValue string                     `json:"operator_value"`
		Parameters    []AttackRuleMatchParameter `json:"parameters"`
		Highlight     []string                   `json:"highlight"`
	}

	// AttackRuleMatchParameter intake API payload.
	AttackRuleMatchParameter struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	// AttackContext intake API payload.
	AttackContext struct {
		Host    AttackContextHost    `json:"host,omitempty"`
		HTTP    AttackContextHTTP    `json:"http"`
		Service AttackContextService `json:"service"`
		Tags    *AttackContextTags   `json:"tags,omitempty"`
		Span    AttackContextSpan    `json:"span"`
		Trace   AttackContextTrace   `json:"trace"`
		Tracer  AttackContextTracer  `json:"tracer"`
	}

	// AttackContextHost intake API payload.
	AttackContextHost struct {
		ContextVersion string `json:"context_version"`
		OsType         string `json:"os_type"`
		Hostname       string `json:"hostname,omitempty"`
	}

	// AttackContextHTTP intake API payload.
	AttackContextHTTP struct {
		ContextVersion string                    `json:"context_version"`
		Request        AttackContextHTTPRequest  `json:"request"`
		Response       AttackContextHTTPResponse `json:"response"`
	}

	// AttackContextHTTPRequest intake API payload.
	AttackContextHTTPRequest struct {
		Scheme     string                             `json:"scheme"`
		Method     string                             `json:"method"`
		URL        string                             `json:"url"`
		Host       string                             `json:"host"`
		Port       int                                `json:"port"`
		Path       string                             `json:"path"`
		Resource   string                             `json:"resource,omitempty"`
		RemoteIP   string                             `json:"remote_ip"`
		RemotePort int                                `json:"remote_port"`
		Headers    map[string]string                  `json:"headers"`
		Parameters AttackContextHTTPRequestParameters `json:"parameters,omitempty"`
	}

	AttackContextHTTPRequestParameters struct {
		Query map[string][]string `json:"query,omitempty"`
	}

	// AttackContextHTTPResponse intake API payload.
	AttackContextHTTPResponse struct {
		Status int `json:"status"`
	}

	// AttackContextService intake API payload.
	AttackContextService struct {
		ContextVersion string `json:"context_version"`
		Name           string `json:"name,omitempty"`
		Environment    string `json:"environment,omitempty"`
		Version        string `json:"version,omitempty"`
	}

	// AttackContextTags intake API payload.
	AttackContextTags struct {
		ContextVersion string   `json:"context_version"`
		Values         []string `json:"values"`
	}

	// AttackContextTrace intake API payload.
	AttackContextTrace struct {
		ContextVersion string `json:"context_version"`
		ID             string `json:"id"`
	}

	// AttackContextSpan intake API payload.
	AttackContextSpan struct {
		ContextVersion string `json:"context_version"`
		ID             string `json:"id"`
	}

	// AttackContextTracer intake API payload.
	AttackContextTracer struct {
		ContextVersion string `json:"context_version"`
		RuntimeType    string `json:"runtime_type"`
		RuntimeVersion string `json:"runtime_version"`
		LibVersion     string `json:"lib_version"`
	}
)

// MakeEventBatch returns the event batch of the given security events.
func MakeEventBatch(events []*AttackEvent) EventBatch {
	id, _ := uuid.NewUUID()
	return EventBatch{
		IdempotencyKey: id.String(),
		Events:         events,
	}
}

// NewAttackEvent returns a new attack event payload.
func NewAttackEvent(ruleID, ruleName, attackType string, at time.Time, match *AttackRuleMatch) *AttackEvent {
	id, _ := uuid.NewUUID()
	return &AttackEvent{
		EventVersion: "0.1.0",
		EventID:      id.String(),
		EventType:    "appsec.threat.attack",
		DetectedAt:   at,
		Type:         attackType,
		Rule: AttackRule{
			ID:   ruleID,
			Name: ruleName,
		},
		RuleMatch: match,
	}
}

// MakeAttackContextTrace create an AttackContextTrace payload.
func MakeAttackContextTrace(traceID string) AttackContextTrace {
	return AttackContextTrace{
		ContextVersion: "0.1.0",
		ID:             traceID,
	}
}

// MakeAttackContextSpan create an AttackContextSpan payload.
func MakeAttackContextSpan(spanID string) AttackContextSpan {
	return AttackContextSpan{
		ContextVersion: "0.1.0",
		ID:             spanID,
	}
}

// MakeAttackContextHost create an AttackContextHost payload.
func MakeAttackContextHost(hostname string, os string) AttackContextHost {
	return AttackContextHost{
		ContextVersion: "0.1.0",
		OsType:         os,
		Hostname:       hostname,
	}
}

// MakeAttackContextTracer create an AttackContextTracer payload.
func MakeAttackContextTracer(version string, rt string, rtVersion string) AttackContextTracer {
	return AttackContextTracer{
		ContextVersion: "0.1.0",
		RuntimeType:    rt,
		RuntimeVersion: rtVersion,
		LibVersion:     version,
	}
}

// NewAttackContextTags create an AttackContextTags payload.
func NewAttackContextTags(tags []string) *AttackContextTags {
	return &AttackContextTags{
		ContextVersion: "0.1.0",
		Values:         tags,
	}
}

// MakeServiceContext create an AttackContextService payload.
func MakeServiceContext(name, version, environment string) AttackContextService {
	return AttackContextService{
		ContextVersion: "0.1.0",
		Name:           name,
		Environment:    environment,
		Version:        version,
	}
}

// MakeAttackContextHTTP create an AttackContextHTTP payload.
func MakeAttackContextHTTP(req AttackContextHTTPRequest, res AttackContextHTTPResponse) AttackContextHTTP {
	return AttackContextHTTP{
		ContextVersion: "0.1.0",
		Request:        req,
		Response:       res,
	}
}

// MakeAttackContextHTTPResponse creates an attackContextHTTPResponse payload.
func MakeAttackContextHTTPResponse(status int) AttackContextHTTPResponse {
	return AttackContextHTTPResponse{
		Status: status,
	}
}

// SplitHostPort splits a network address of the form `host:port` or
// `[host]:port` into `host` and `port`. As opposed to `net.SplitHostPort()`,
// it doesn't fail when there is no port number and returns the given address
// as the host value.
func SplitHostPort(addr string) (host, port string) {
	addr = strings.TrimSpace(addr)
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		return
	}
	if l := len(addr); l >= 2 && addr[0] == '[' && addr[l-1] == ']' {
		// ipv6 without port number
		return addr[1 : l-1], ""
	}
	return addr, ""
}

// List of HTTP headers we collect and send.
var collectedHTTPHeaders = [...]string{
	"host",
	"x-forwarded-for",
	"x-client-ip",
	"x-real-ip",
	"x-forwarded",
	"x-cluster-client-ip",
	"forwarded-for",
	"forwarded",
	"via",
	"true-client-ip",
	"content-length",
	"content-type",
	"content-encoding",
	"content-language",
	"forwarded",
	"user-agent",
	"accept",
	"accept-encoding",
	"accept-language",
}

func init() {
	// Required by sort.SearchStrings
	sort.Strings(collectedHTTPHeaders[:])
}

// MakeHTTPHeaders returns the HTTP headers following the intake payload format.
func MakeHTTPHeaders(reqHeaders map[string][]string) (headers map[string]string) {
	if len(reqHeaders) == 0 {
		return nil
	}
	headers = make(map[string]string)
	for k, v := range reqHeaders {
		if i := sort.SearchStrings(collectedHTTPHeaders[:], k); i < len(collectedHTTPHeaders) && collectedHTTPHeaders[i] == k {
			headers[k] = strings.Join(v, ";")
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// MakeHTTPURL returns the HTTP URL from the given scheme, host and path.
func MakeHTTPURL(scheme, host, path string) string {
	return fmt.Sprintf("%s://%s%s", scheme, host, path)
}
