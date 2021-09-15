package api

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/appsec/internal/protection/waf"
	appsectypes "github.com/DataDog/dd-trace-go/appsec/types"

	"github.com/google/uuid"
)

type (
	AttackEvent struct {
		EventID      string           `json:"event_id"`
		EventType    string           `json:"event_type"`
		EventVersion string           `json:"event_version"`
		DetectedAt   time.Time        `json:"detected_at"`
		Type         string           `json:"type"`
		Blocked      bool             `json:"blocked"`
		Rule         *AttackRule      `json:"rule"`
		RuleMatch    *AttackRuleMatch `json:"rule_match"`
		Context      *AttackContext   `json:"context"`
	}

	AttackRule struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	AttackRuleMatch struct {
		Operator      string                     `json:"operator"`
		OperatorValue string                     `json:"operator_value"`
		Parameters    []AttackRuleMatchParameter `json:"parameters"`
		Highlight     []string                   `json:"highlight"`
	}

	AttackRuleMatchParameter struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

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

	AttackContextActor struct {
		ContextVersion string               `json:"context_version"`
		IP             AttackContextActorIP `json:"ip"`
	}

	AttackContextActorIP struct {
		Address string `json:"address"`
	}

	AttackContextHost struct {
		ContextVersion string `json:"context_version"`
		OsType         string `json:"os_type"`
		Hostname       string `json:"hostname"`
	}

	AttackContextHTTP struct {
		ContextVersion string                    `json:"context_version"`
		Request        AttackContextHTTPRequest  `json:"request"`
		Response       AttackContextHTTPResponse `json:"response"`
	}

	AttackContextHTTPRequest struct {
		Scheme     string `json:"scheme"`
		Method     string `json:"method"`
		URL        string `json:"url"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		Path       string `json:"path"`
		Resource   string `json:"resource"`
		RemoteIP   string `json:"remote_ip"`
		RemotePort int    `json:"remote_port"`
	}

	AttackContextHTTPResponse struct {
		Status int `json:"status"`
	}

	AttackContextService struct {
		ContextVersion string `json:"context_version"`
		Name           string `json:"name"`
		Environment    string `json:"environment"`
		Version        string `json:"version"`
	}

	AttackContextTags struct {
		ContextVersion string   `json:"context_version"`
		Values         []string `json:"values"`
	}

	AttackContextTrace struct {
		ContextVersion string `json:"context_version"`
		ID             string `json:"id"`
	}

	AttackContextSpan struct {
		ContextVersion string `json:"context_version"`
		ID             string `json:"id"`
	}

	AttackContextTracer struct {
		ContextVersion string `json:"context_version"`
		RuntimeType    string `json:"runtime_type"`
		RuntimeVersion string `json:"runtime_version"`
		LibVersion     string `json:"lib_version"`
	}
)

func NewAttackEvent(attackType string, blocked bool, at time.Time, rule *AttackRule, match *AttackRuleMatch, attackCtx *AttackContext) *AttackEvent {
	id, _ := uuid.NewUUID()
	return &AttackEvent{
		EventID:      id.String(),
		EventType:    "appsec.threat.attack",
		EventVersion: "0.1.0",
		DetectedAt:   at,
		Type:         attackType,
		Blocked:      blocked,
		Rule:         rule,
		RuleMatch:    match,
		Context:      attackCtx,
	}
}

func FromWAFAttack(t time.Time, blocked bool, md []byte, attackContext *AttackContext) ([]*AttackEvent, error) {
	var waf waf.AttackMetadata
	if err := json.Unmarshal(md, &waf); err != nil {
		return nil, err
	}
	rule := &AttackRule{
		ID:   waf[0].Rule,
		Name: waf[0].Flow,
	}
	match := &AttackRuleMatch{
		Operator:      waf[0].Filter[0].Operator,
		OperatorValue: waf[0].Filter[0].OperatorValue,
		Parameters: []AttackRuleMatchParameter{
			{
				Name:  waf[0].Filter[0].BindingAccessor,
				Value: waf[0].Filter[0].ResolvedValue,
			},
		},
		Highlight: []string{waf[0].Filter[0].MatchStatus},
	}
	attack := NewAttackEvent(waf[0].Flow, blocked, t, rule, match, attackContext)
	return []*AttackEvent{attack}, nil
}

func (*AttackEvent) isEvent() {}

type (
	EventBatch struct {
		IdempotencyKey string  `json:"idempotency_key"`
		Events         []Event `json:"events"`
	}
	Event interface {
		isEvent()
	}
)

func FromSecurityEvents(events []*appsectypes.SecurityEvent, globalContext []appsectypes.SecurityEventContext) EventBatch {
	id, _ := uuid.NewUUID()
	var batch = EventBatch{
		IdempotencyKey: id.String(),
		Events:         make([]Event, 0, len(events)),
	}

	for _, event := range events {
		eventContext := NewAttackContext(event.Context, globalContext)
		switch actual := event.Event.(type) {
		case []waf.RawAttackMetadata:
			for _, attack := range actual {
				attacks, _ := FromWAFAttack(attack.Time, attack.Block, attack.Metadata, eventContext)
				// TODO: handle the previous error
				for _, attack := range attacks {
					batch.Events = append(batch.Events, attack)
				}
			}
		}
	}
	return batch
}

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
	case appsectypes.HTTPOperationContext:
		c.applyHTTPOperationContext(actual)
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

func MakeAttackContextTrace(traceID string) AttackContextTrace {
	return AttackContextTrace{
		ContextVersion: "0.1.0",
		ID:             traceID,
	}
}

func MakeAttackContextSpan(spanID string) AttackContextSpan {
	return AttackContextSpan{
		ContextVersion: "0.1.0",
		ID:             spanID,
	}
}

func (c *AttackContext) applyHTTPOperationContext(ctx appsectypes.HTTPOperationContext) {
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

func MakeAttackContextHost(hostname string, os string) AttackContextHost {
	return AttackContextHost{
		ContextVersion: "0.1.0",
		OsType:         os,
		Hostname:       hostname,
	}
}

func MakeAttackContextTracer(version string, rt string, rtVersion string) AttackContextTracer {
	return AttackContextTracer{
		ContextVersion: "0.1.0",
		RuntimeType:    rt,
		RuntimeVersion: rtVersion,
		LibVersion:     version,
	}
}

func MakeAttackContextTags(tags []string) AttackContextTags {
	return AttackContextTags{
		ContextVersion: "0.1.0",
		Values:         tags,
	}
}

func MakeServiceContext(name string, version string, environment string) AttackContextService {
	return AttackContextService{
		ContextVersion: "0.1.0",
		Name:           name,
		Environment:    environment,
		Version:        version,
	}
}

func MakeAttackContextHTTPResponse(res appsectypes.HTTPResponseContext) AttackContextHTTPResponse {
	return AttackContextHTTPResponse{
		Status: res.Status,
	}
}

func MakeAttackContextHTTP(req AttackContextHTTPRequest, res AttackContextHTTPResponse) AttackContextHTTP {
	return AttackContextHTTP{
		ContextVersion: "0.1.0",
		Request:        req,
		Response:       res,
	}
}

func MakeAttackContextHTTPRequest(req appsectypes.HTTPRequestContext) AttackContextHTTPRequest {
	host, portStr := splitHostPort(req.Host)
	remoteIP, remotePortStr := splitHostPort(req.RemoteAddr)
	port, _ := strconv.Atoi(portStr)
	remotePort, _ := strconv.Atoi(remotePortStr)
	var scheme string
	if req.IsTLS {
		scheme = "https"
	} else {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://%s%s", scheme, req.Host, req.RequestURI)
	return AttackContextHTTPRequest{
		Scheme:     scheme,
		Method:     req.Method,
		URL:        url,
		Host:       host,
		Port:       port,
		Path:       req.RequestURI,
		RemoteIP:   remoteIP,
		RemotePort: remotePort,
	}
}

func MakeAttackContextActor(ip string) AttackContextActor {
	return AttackContextActor{
		ContextVersion: "0.1.0",
		IP:             AttackContextActorIP{Address: ip},
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
