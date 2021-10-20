// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

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
	// eventBatch intake API payload.
	eventBatch struct {
		IdempotencyKey string         `json:"idempotency_key"`
		Events         []*attackEvent `json:"events"`
	}

	// attackEvent intake API payload.
	attackEvent struct {
		EventVersion string           `json:"event_version"`
		EventID      string           `json:"event_id"`
		EventType    string           `json:"event_type"`
		DetectedAt   time.Time        `json:"detected_at"`
		Type         string           `json:"type"`
		Blocked      bool             `json:"blocked"`
		Rule         attackRule       `json:"rule"`
		RuleMatch    *attackRuleMatch `json:"rule_match"`
		Context      attackContext    `json:"context"`
	}

	// attackRule intake API payload.
	attackRule struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	// attackRuleMatch intake API payload.
	attackRuleMatch struct {
		Operator      string                     `json:"operator"`
		OperatorValue string                     `json:"operator_value"`
		Parameters    []attackRuleMatchParameter `json:"parameters"`
		Highlight     []string                   `json:"highlight"`
	}

	// attackRuleMatchParameter intake API payload.
	attackRuleMatchParameter struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	// attackContext intake API payload.
	attackContext struct {
		Host    attackContextHost    `json:"host,omitempty"`
		HTTP    attackContextHTTP    `json:"http"`
		Service attackContextService `json:"service"`
		Tags    *attackContextTags   `json:"tags,omitempty"`
		Span    attackContextSpan    `json:"span"`
		Trace   attackContextTrace   `json:"trace"`
		Tracer  attackContextTracer  `json:"tracer"`
	}

	// attackContextHost intake API payload.
	attackContextHost struct {
		ContextVersion string `json:"context_version"`
		OsType         string `json:"os_type"`
		Hostname       string `json:"hostname,omitempty"`
	}

	// attackContextHTTP intake API payload.
	attackContextHTTP struct {
		ContextVersion string                    `json:"context_version"`
		Request        attackContextHTTPRequest  `json:"request"`
		Response       attackContextHTTPResponse `json:"response"`
	}

	// attackContextHTTPRequest intake API payload.
	attackContextHTTPRequest struct {
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
		Parameters attackContextHTTPRequestParameters `json:"parameters,omitempty"`
	}

	attackContextHTTPRequestParameters struct {
		Query map[string][]string `json:"query,omitempty"`
	}

	// attackContextHTTPResponse intake API payload.
	attackContextHTTPResponse struct {
		Status int `json:"status"`
	}

	// attackContextService intake API payload.
	attackContextService struct {
		ContextVersion string `json:"context_version"`
		Name           string `json:"name,omitempty"`
		Environment    string `json:"environment,omitempty"`
		Version        string `json:"version,omitempty"`
	}

	// attackContextTags intake API payload.
	attackContextTags struct {
		ContextVersion string   `json:"context_version"`
		Values         []string `json:"values"`
	}

	// attackContextTrace intake API payload.
	attackContextTrace struct {
		ContextVersion string `json:"context_version"`
		ID             string `json:"id"`
	}

	// attackContextSpan intake API payload.
	attackContextSpan struct {
		ContextVersion string `json:"context_version"`
		ID             string `json:"id"`
	}

	// attackContextTracer intake API payload.
	attackContextTracer struct {
		ContextVersion string `json:"context_version"`
		RuntimeType    string `json:"runtime_type"`
		RuntimeVersion string `json:"runtime_version"`
		LibVersion     string `json:"lib_version"`
	}
)

// makeEventBatch returns the event batch of the given security events.
func makeEventBatch(events []*attackEvent) eventBatch {
	id, _ := uuid.NewUUID()
	return eventBatch{
		IdempotencyKey: id.String(),
		Events:         events,
	}
}

// newAttackEvent returns a new attack event payload.
func newAttackEvent(ruleID, ruleName, attackType string, at time.Time, match *attackRuleMatch) *attackEvent {
	id, _ := uuid.NewUUID()
	return &attackEvent{
		EventVersion: "0.1.0",
		EventID:      id.String(),
		EventType:    "appsec.threat.attack",
		DetectedAt:   at,
		Type:         attackType,
		Rule: attackRule{
			ID:   ruleID,
			Name: ruleName,
		},
		RuleMatch: match,
	}
}

// makeAttackContextTrace create an attackContextTrace payload.
func makeAttackContextTrace(traceID string) attackContextTrace {
	return attackContextTrace{
		ContextVersion: "0.1.0",
		ID:             traceID,
	}
}

// makeAttackContextSpan create an attackContextSpan payload.
func makeAttackContextSpan(spanID string) attackContextSpan {
	return attackContextSpan{
		ContextVersion: "0.1.0",
		ID:             spanID,
	}
}

// makeAttackContextHost create an attackContextHost payload.
func makeAttackContextHost(hostname string, os string) attackContextHost {
	return attackContextHost{
		ContextVersion: "0.1.0",
		OsType:         os,
		Hostname:       hostname,
	}
}

// makeAttackContextTracer create an attackContextTracer payload.
func makeAttackContextTracer(version string, rt string, rtVersion string) attackContextTracer {
	return attackContextTracer{
		ContextVersion: "0.1.0",
		RuntimeType:    rt,
		RuntimeVersion: rtVersion,
		LibVersion:     version,
	}
}

// newAttackContextTags create an attackContextTags payload.
func newAttackContextTags(tags []string) *attackContextTags {
	return &attackContextTags{
		ContextVersion: "0.1.0",
		Values:         tags,
	}
}

// makeServiceContext create an attackContextService payload.
func makeServiceContext(name, version, environment string) attackContextService {
	return attackContextService{
		ContextVersion: "0.1.0",
		Name:           name,
		Environment:    environment,
		Version:        version,
	}
}

// makeAttackContextHTTP create an attackContextHTTP payload.
func makeAttackContextHTTP(req attackContextHTTPRequest, res attackContextHTTPResponse) attackContextHTTP {
	return attackContextHTTP{
		ContextVersion: "0.1.0",
		Request:        req,
		Response:       res,
	}
}

// makeAttackContextHTTPResponse creates an attackContextHTTPResponse payload.
func makeAttackContextHTTPResponse(status int) attackContextHTTPResponse {
	return attackContextHTTPResponse{
		Status: status,
	}
}

// splitHostPort splits a network address of the form `host:port` or
// `[host]:port` into `host` and `port`. As opposed to `net.SplitHostPort()`,
// it doesn't fail when there is no port number and returns the given address
// as the host value.
func splitHostPort(addr string) (host, port string) {
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

// makeHTTPHeaders returns the HTTP headers following the intake payload format.
func makeHTTPHeaders(reqHeaders map[string][]string) (headers map[string]string) {
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

// makeHTTPURL returns the HTTP URL from the given scheme, host and path.
func makeHTTPURL(scheme, host, path string) string {
	return fmt.Sprintf("%s://%s%s", scheme, host, path)
}
