// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package api

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	waftypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/types"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/google/uuid"
)

// Intake API payloads.
type (
	// AttackEvent intake API payload.
	AttackEvent struct {
		EventID      string           `json:"event_id"`
		EventType    string           `json:"event_type"`
		EventVersion string           `json:"event_version"`
		DetectedAt   time.Time        `json:"detected_at"`
		Type         string           `json:"type"`
		Rule         *AttackRule      `json:"rule"`
		RuleMatch    *AttackRuleMatch `json:"rule_match"`
		Context      *AttackContext   `json:"context"`
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
		Actor   *AttackContextActor  `json:"actor,omitempty"`
		Host    AttackContextHost    `json:"host"`
		HTTP    AttackContextHTTP    `json:"http"`
		Service AttackContextService `json:"service"`
		Tags    AttackContextTags    `json:"tags"`
		Span    AttackContextSpan    `json:"span"`
		Trace   AttackContextTrace   `json:"trace"`
		Tracer  AttackContextTracer  `json:"tracer"`
	}

	// AttackContextActor intake API payload.
	AttackContextActor struct {
		ContextVersion string               `json:"context_version"`
		IP             AttackContextActorIP `json:"ip"`
	}

	// AttackContextActorIP intake API payload.
	AttackContextActorIP struct {
		Address string `json:"address"`
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
		Query map[string]string `json:"query,omitempty"`
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

// NewAttackEvent returns a new attack event payload.
func NewAttackEvent(attackType string, at time.Time, rule *AttackRule, match *AttackRuleMatch, attackCtx *AttackContext) *AttackEvent {
	id, _ := uuid.NewUUID()
	return &AttackEvent{
		EventID:      id.String(),
		EventType:    "appsec.threat.attack",
		EventVersion: "0.1.0",
		DetectedAt:   at,
		Type:         attackType,
		Rule:         rule,
		RuleMatch:    match,
		Context:      attackCtx,
	}
}

// FromWAFAttack creates the attack event payloads from a WAF attack.
func FromWAFAttack(t time.Time, md []byte, attackContext *AttackContext) (events []*AttackEvent, err error) {
	var matches waftypes.AttackMetadata
	if err := json.Unmarshal(md, &matches); err != nil {
		return nil, err
	}
	// Create one security event per flow and per filter
	for _, match := range matches {
		rule := &AttackRule{
			ID:   match.Rule,
			Name: match.Flow,
		}
		for _, filter := range match.Filter {
			ruleMatch := &AttackRuleMatch{
				Operator:      filter.Operator,
				OperatorValue: filter.OperatorValue,
				Parameters: []AttackRuleMatchParameter{
					{
						Name:  filter.BindingAccessor,
						Value: filter.ResolvedValue,
					},
				},
				Highlight: []string{filter.MatchStatus},
			}
			events = append(events, NewAttackEvent(match.Flow, t, rule, ruleMatch, attackContext))
		}
	}
	return events, nil
}

func (*AttackEvent) isEvent() {}

type (
	// EventBatch intake API payload.
	EventBatch struct {
		IdempotencyKey string  `json:"idempotency_key"`
		Events         []Event `json:"events"`
	}
	// Event interface that batchable events must implement.
	Event interface {
		isEvent()
	}
)

// FromSecurityEvents returns the event batch of the given security events. The given global event context is added
// to each newly created AttackEvent as AttackContext.
func FromSecurityEvents(events []*appsectypes.SecurityEvent, globalContext []appsectypes.SecurityEventContext) EventBatch {
	id, _ := uuid.NewUUID()
	var batch = EventBatch{
		IdempotencyKey: id.String(),
		Events:         make([]Event, 0, len(events)),
	}
	for _, event := range events {
		eventContext := NewAttackContext(event.Context, globalContext)
		switch actual := event.Event.(type) {
		case []waftypes.RawAttackMetadata:
			for _, attack := range actual {
				attacks, err := FromWAFAttack(attack.Time, attack.Metadata, eventContext)
				if err != nil {
					log.Error("appsec: could not create the security event payload out of a waf attack: %v", err)
					continue
				}
				for _, attack := range attacks {
					batch.Events = append(batch.Events, attack)
				}
			}
		}
	}
	return batch
}

// NewAttackContext creates and returns a new attack context from the given security event contexts. The event local
// and global contexts are separated to avoid allocating a temporary slice merging both - the caller can keep them
// separate without appending them for the time of the call.
func NewAttackContext(ctx, globalCtx []appsectypes.SecurityEventContext) *AttackContext {
	aCtx := &AttackContext{}
	for _, ctx := range ctx {
		aCtx.applyContext(ctx)
	}
	for _, ctx := range globalCtx {
		aCtx.applyContext(ctx)
	}
	return aCtx
}

func (c *AttackContext) applyContext(ctx appsectypes.SecurityEventContext) {
	switch actual := ctx.(type) {
	case appsectypes.SpanContext:
		c.applySpanContext(actual)
	case appsectypes.HTTPContext:
		c.applyHTTPContext(actual)
	case appsectypes.ServiceContext:
		c.applyServiceContext(actual)
	case appsectypes.TagContext:
		c.applyTagContext(actual)
	case appsectypes.TracerContext:
		c.applyTracerContext(actual)
	case appsectypes.HostContext:
		c.applyHostContext(actual)
	}
}

func (c *AttackContext) applySpanContext(ctx appsectypes.SpanContext) {
	trace := strconv.FormatUint(ctx.TraceID, 10)
	span := strconv.FormatUint(ctx.TraceID, 10)
	c.Trace = MakeAttackContextTrace(trace)
	c.Span = MakeAttackContextSpan(span)
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

func (c *AttackContext) applyHTTPContext(ctx appsectypes.HTTPContext) {
	c.HTTP = MakeAttackContextHTTP(MakeAttackContextHTTPRequest(ctx.Request), MakeAttackContextHTTPResponse(ctx.Response))
}

func (c *AttackContext) applyServiceContext(ctx appsectypes.ServiceContext) {
	c.Service = MakeServiceContext(ctx.Name, ctx.Version, ctx.Environment)
}

func (c *AttackContext) applyTagContext(ctx appsectypes.TagContext) {
	c.Tags = MakeAttackContextTags(ctx)
}

func (c *AttackContext) applyTracerContext(ctx appsectypes.TracerContext) {
	c.Tracer = MakeAttackContextTracer(ctx.Version, ctx.Runtime, ctx.RuntimeVersion)
}

func (c *AttackContext) applyHostContext(ctx appsectypes.HostContext) {
	c.Host = MakeAttackContextHost(ctx.Hostname, ctx.OS)
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

// MakeAttackContextTags create an AttackContextTags payload.
func MakeAttackContextTags(tags []string) AttackContextTags {
	return AttackContextTags{
		ContextVersion: "0.1.0",
		Values:         tags,
	}
}

// MakeServiceContext create an AttackContextService payload.
func MakeServiceContext(name string, version string, environment string) AttackContextService {
	return AttackContextService{
		ContextVersion: "0.1.0",
		Name:           name,
		Environment:    environment,
		Version:        version,
	}
}

// MakeAttackContextHTTPResponse create an AttackContextHTTPResponse payload.
func MakeAttackContextHTTPResponse(res appsectypes.HTTPResponseContext) AttackContextHTTPResponse {
	return AttackContextHTTPResponse{
		Status: res.Status,
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

var collectedHeaders = [...]string{
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

// MakeAttackContextHTTPRequest create an AttackContextHTTPRequest payload.
func MakeAttackContextHTTPRequest(req appsectypes.HTTPRequestContext) AttackContextHTTPRequest {
	host, portStr := splitHostPort(req.Host)
	port, _ := strconv.Atoi(portStr)

	remoteIP, remotePortStr := splitHostPort(req.RemoteAddr)
	remotePort, _ := strconv.Atoi(remotePortStr)

	var scheme string
	if req.IsTLS {
		scheme = "https"
	} else {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s%s", scheme, req.Host, req.Path)

	var headers map[string]string
	if l := len(req.Headers); l > 0 {
		headers = make(map[string]string)
		for _, k := range collectedHeaders {
			if v, ok := req.Headers[k]; ok {
				headers[k] = strings.Join(v, ";")
			}
		}
	}

	var query map[string]string
	if l := len(req.Query); l > 0 {
		query = make(map[string]string, l)
		for k, v := range req.Query {
			query[k] = strings.Join(v, ";")
		}
	}

	return AttackContextHTTPRequest{
		Scheme:     scheme,
		Method:     req.Method,
		URL:        url,
		Host:       host,
		Port:       port,
		Path:       req.Path,
		RemoteIP:   remoteIP,
		RemotePort: remotePort,
		Headers:    headers,
		Parameters: AttackContextHTTPRequestParameters{Query: query},
	}
}

// splitHostPort splits a network address of the form `host:port` or
// `[host]:port` into `host` and `port`. As opposed to `net.SplitHostPort()`,
// it doesn't fail when there is no port number and returns the given address
// as the host value.
func splitHostPort(addr string) (host string, port string) {
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
