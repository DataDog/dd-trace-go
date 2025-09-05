// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"context"
	"errors"
	"slices"
	"sync"
	"unicode"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/llmobs/internal/config"
	"github.com/DataDog/dd-trace-go/v2/llmobs/internal/dne"
)

var (
	mu           sync.Mutex
	activeLLMObs *LLMObs
)

var (
	errLLMObsNotEnabled        = errors.New("LLMObs is not enabled. Ensure the experiment has been correctly initialized using experiment.New and llmobs.Start() has been called or set DD_LLMOBS_ENABLED=1")
	errAgentlessRequiresAPIKey = errors.New("LLMOBs agentless mode requires a valid API key - set the DD_API_KEY env variable to configure one")
	errMLAppRequired           = errors.New("ML App is required for sending LLM Observability data")
)

const (
	baggageKeyExperimentID = "_ml_obs.experiment_id"
)

type OperationKind string

const (
	OperationKindExperiment = "experiment"
)

// See: https://docs.datadoghq.com/getting_started/site/#access-the-datadog-site
var ddSitesNeedingAppSubdomain = []string{"datadoghq.com", "datadoghq.eu", "ddog-gov.com"}

const (
	keySpanKind                = "_ml_obs.meta.span.kind"
	keySessionID               = "_ml_obs.session_id"
	keyMetadata                = "_ml_obs.meta.metadata"
	keyMetrics                 = "_ml_obs.metrics"
	keyMLApp                   = "_ml_obs.meta.ml_app"
	keyPropagatedParentID      = "_dd.p.llmobs_parent_id"
	keyPropagatedMLAPP         = "_dd.p.llmobs_ml_app"
	keyParentID                = "_ml_obs.llmobs_parent_id"
	keyPropagatedLLMObsTraceID = "_dd.p.llmobs_trace_id"
	keyLLMObsTraceID           = "_ml_obs.llmobs_trace_id"
	keyTags                    = "_ml_obs.tags"
	keyAgentManifest           = "_ml_obs.meta.agent_manifest"

	keyModelName     = "_ml_obs.meta.model_name"
	keyModelProvider = "_ml_obs.meta.model_provider"

	keyInputDocuments  = "_ml_obs.meta.input.documents"
	keyInputMessages   = "_ml_obs.meta.input.messages"
	keyInputValue      = "_ml_obs.meta.input.value"
	keyInputPrompt     = "_ml_obs.meta.input.prompt"
	keyToolDefinitions = "_ml_obs.meta.tool_definitions"

	keyOutputDocuments = "_ml_obs.meta.output.documents"
	keyOutputMessages  = "_ml_obs.meta.output.messages"
	keyOutputValue     = "_ml_obs.meta.output.value"
)

type llmobsContext struct {
	spanKind           string
	sessionID          string
	metadata           map[string]any
	metrics            map[string]any
	mlApp              string
	propagatedParentID string
	propagatedMLApp    string
	parentID           string
	propagatedTraceID  string
	traceID            string
	tags               map[string]any
	agentManifest      string
	modelName          string
	modelProvider      string
}

type LLMObs struct {
	Config    *config.Config
	DNEClient *dne.Client
}

func newLLMObs(opts ...Option) (*LLMObs, error) {
	cfg := config.Default()
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.AgentlessEnabled && !isAPIKeyValid(cfg.APIKey) {
		return nil, errAgentlessRequiresAPIKey
	}
	if cfg.MLApp == "" {
		return nil, errMLAppRequired
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = cfg.DefaultHTTPClient()
	}
	return &LLMObs{Config: cfg, DNEClient: dne.NewClient(cfg)}, nil
}

func Start(opts ...Option) error {
	mu.Lock()
	defer mu.Unlock()

	if activeLLMObs != nil {
		activeLLMObs.Stop()
	}
	l, err := newLLMObs(opts...)
	if err != nil {
		return err
	}
	if !l.Config.Enabled {
		return nil
	}
	activeLLMObs = l
	activeLLMObs.Run()
	return nil
}

func Stop() {
	mu.Lock()
	defer mu.Unlock()

	if activeLLMObs != nil {
		activeLLMObs.Stop()
		activeLLMObs = nil
	}
}

func ActiveLLMObs() (*LLMObs, error) {
	if activeLLMObs == nil || !activeLLMObs.Config.Enabled {
		return nil, errLLMObsNotEnabled
	}
	return activeLLMObs, nil
}

func (l *LLMObs) Stop() {
	//TODO
}

func (l *LLMObs) Run() {
	//TODO
}

func (l *LLMObs) Flush() {
	//TODO
	tracer.Flush()
}

type Span struct {
	apm       *tracer.Span
	parent    *Span
	llmobsCtx *llmobsContext
}

func (s *Span) SpanID() uint64 {
	return s.apm.Context().SpanID()
}

func (s *Span) TraceID() string {
	return s.apm.Context().TraceID()
}

func (s *Span) Finish(opts ...tracer.FinishOption) {
	s.apm.Finish(opts...)
	l, err := ActiveLLMObs()
	if err != nil {
		return
	}
	l.submitLLMObsSpan(s)
	//def _on_span_finish(self, span: Span) -> None:
	//if self.enabled and span.span_type == SpanTypes.LLM:
	//	self._submit_llmobs_span(span)
	//	telemetry.record_span_created(span)
}

func (s *Span) StartChild(name string, opts ...tracer.StartSpanOption) *Span {
	return &Span{
		apm:    s.apm.StartChild(name, opts...),
		parent: s,
	}
}

func (l *LLMObs) submitLLMObsSpan(span *Span) {
	// TODO
}

func (l *LLMObs) StartSpan(ctx context.Context, opKind OperationKind, name string, opts ...StartSpanOption) (*Span, context.Context) {
	spanName := name
	if spanName == "" {
		spanName = string(opKind)
	}

	cfg := &startSpanConfig{
		sessionID:     "",
		modelName:     "",
		modelProvider: "",
		mlApp:         "",
		startSpanOpts: nil,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.mlApp == "" {
		cfg.mlApp = l.Config.MLApp
	}

	spanOpts := make([]tracer.StartSpanOption, 0, len(cfg.startSpanOpts)+1)
	spanOpts = append(spanOpts, cfg.startSpanOpts...)
	spanOpts = append(spanOpts, tracer.SpanType(ext.SpanTypeLLM))

	apmSpan, ctx := tracer.StartSpanFromContext(ctx, name, spanOpts...)
	span := &Span{apm: apmSpan}
	if !l.Config.Enabled {
		log.Warn("llmobs: LLMObs span was started without enabling LLMObs")
		return span, ctx
	}

	// TODO(rarguelloF): set a bunch of info in the span context
	span.llmobsCtx = &llmobsContext{
		spanKind:           string(opKind),
		metadata:           nil,
		metrics:            nil,
		mlApp:              "",
		propagatedParentID: "",
		propagatedMLApp:    "",
		parentID:           "",
		propagatedTraceID:  "",
		traceID:            "",
		tags:               nil,
		agentManifest:      "",
		modelName:          cfg.modelName,
		modelProvider:      cfg.modelProvider,
	}
	if cfg.sessionID != "" {
		span.llmobsCtx.sessionID = cfg.sessionID
	} else if sessionID := span.sessionID(); sessionID != "" {
		span.llmobsCtx.sessionID = sessionID
	}

	//if model_provider is not None:
	//span._set_ctx_item(MODEL_PROVIDER, model_provider)
	//session_id = session_id if session_id is not None else _get_session_id(span)
	//if session_id is not None:
	//span._set_ctx_item(SESSION_ID, session_id)
	//
	//ml_app = ml_app if ml_app is not None else _get_ml_app(span)
	//if ml_app is None:
	//raise ValueError(
	//	"ml_app is required for sending LLM Observability data. "
	//"Ensure the name of your LLM application is set via `DD_LLMOBS_ML_APP` or `LLMObs.enable(ml_app='...')`"
	//"before running your application."
	//)
	//span._set_ctx_items({DECORATOR: _decorator, SPAN_KIND: operation_kind, ML_APP: ml_app})

	log.Debug("llmobs: starting LLMObs span: %s, span_kind: %s, ml_app: %s", name, opKind, cfg.mlApp)
	return span, ctx
}

type SpanAnnotations struct {
	Prompt     map[string]any
	InputData  any
	OutputData any
	Metadata   map[string]any
	Metrics    map[string]any
	Tags       map[string]any
}

func (l *LLMObs) AnnotateSpan(span *Span, annotations SpanAnnotations) {
	// TODO(rarguelloF)
}

func (l *LLMObs) StartExperimentSpan(ctx context.Context, name string, experimentID string, opts ...StartSpanOption) (*Span, context.Context) {
	span, ctx := l.StartSpan(ctx, OperationKindExperiment, name, opts...)

	if experimentID != "" {
		span.apm.SetBaggageItem(baggageKeyExperimentID, experimentID)
	}
	return span, ctx
}

func ResourceBaseURL() string {
	site := "datadoghq.com"
	if activeLLMObs != nil {
		site = activeLLMObs.Config.Site
	}

	baseURL := "https://"
	if slices.Contains(ddSitesNeedingAppSubdomain, site) {
		baseURL += "app."
	}
	baseURL += site
	return baseURL
}

// isAPIKeyValid reports whether the given string is a structurally valid API key
func isAPIKeyValid(key string) bool {
	if len(key) != 32 {
		return false
	}
	for _, c := range key {
		if c > unicode.MaxASCII || (!unicode.IsLower(c) && !unicode.IsNumber(c)) {
			return false
		}
	}
	return true
}

func (s *Span) sessionID() string {
	curSpan := s

	for curSpan != nil {
		if curSpan.llmobsCtx != nil && curSpan.llmobsCtx.sessionID != "" {
			return curSpan.llmobsCtx.sessionID
		}
		curSpan = curSpan.parent
	}
	return ""
}

//def _get_session_id(span: Span) -> Optional[str]:
//"""Return the session ID for a given span, by checking the span's nearest LLMObs span ancestor."""
//session_id = span._get_ctx_item(SESSION_ID)
//if session_id:
//return session_id
//llmobs_parent = _get_nearest_llmobs_ancestor(span)
//while llmobs_parent:
//session_id = llmobs_parent._get_ctx_item(SESSION_ID)
//if session_id is not None:
//return session_id
//llmobs_parent = _get_nearest_llmobs_ancestor(llmobs_parent)
//return session_id
