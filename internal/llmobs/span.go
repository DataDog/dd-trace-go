// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	TagKeySessionID = "session_id"
)

type Span struct {
	mu sync.RWMutex

	apm          APMSpan
	parent       *Span
	propagated   *PropagatedLLMSpan
	llmCtx       llmobsContext
	llmTraceID   string
	name         string
	mlApp        string
	integration  string
	scope        string
	isEvaluation bool
	error        error
	finished     bool
	startTime    time.Time
	finishTime   time.Time
	spanLinks    []SpanLink
}

func (s *Span) SpanID() string {
	return s.apm.SpanID()
}

func (s *Span) APMTraceID() string {
	return s.apm.TraceID()
}

func (s *Span) TraceID() string {
	return s.llmTraceID
}

func (s *Span) MLApp() string {
	return s.mlApp
}

func (s *Span) AddLink(link SpanLink) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.apm.AddLink(link)
	s.spanLinks = append(s.spanLinks, link)
}

func (s *Span) StartTime() time.Time {
	return s.startTime
}

func (s *Span) FinishTime() time.Time {
	return s.finishTime
}

func (s *Span) Finish(cfg FinishSpanConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		log.Debug("llmobs: attempted to finish an already finished span")
		return
	}

	if cfg.FinishTime.IsZero() {
		cfg.FinishTime = time.Now()
	}
	s.finishTime = cfg.FinishTime
	apmFinishCfg := FinishAPMSpanConfig{
		FinishTime: cfg.FinishTime,
	}
	if cfg.Error != nil {
		s.error = cfg.Error
		apmFinishCfg.Error = cfg.Error
	}

	s.apm.Finish(apmFinishCfg)
	l, err := ActiveLLMObs()
	if err != nil {
		return
	}
	l.submitLLMObsSpan(s)
	s.finished = true

	//TODO: telemetry.record_span_created(span)
}

type Prompt struct {
	Template            string            `json:"template,omitempty"`
	ID                  string            `json:"id,omitempty"`
	Version             string            `json:"version,omitempty"`
	Variables           map[string]string `json:"variables,omitempty"`
	RAGContextVariables []string          `json:"rag_context_variables,omitempty"`
	RAGQueryVariables   []string          `json:"rag_query_variables,omitempty"`
}

type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	ToolID    string          `json:"tool_id,omitempty"`
	Type      string          `json:"type,omitempty"`
}

type ToolResult struct {
	Result any    `json:"result"`
	Name   string `json:"name,omitempty"`
	ToolID string `json:"tool_id,omitempty"`
	Type   string `json:"type,omitempty"`
}

type LLMMessage struct {
	Role        string       `json:"role"`
	Content     string       `json:"content"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

type EmbeddedDocument struct {
	Text string `json:"text"`
}

type RetrievedDocument struct {
	Text  string  `json:"text"`
	Name  string  `json:"name,omitempty"`
	Score float64 `json:"score,omitempty"`
	ID    string  `json:"id,omitempty"`
}

type SpanAnnotations struct {
	InputText         string
	InputMessages     []LLMMessage
	InputEmbeddedDocs []EmbeddedDocument

	OutputText          string
	OutputMessages      []LLMMessage
	OutputRetrievedDocs []RetrievedDocument

	ExperimentInput          map[string]any
	ExperimentOutput         any
	ExperimentExpectedOutput any

	Prompt        *Prompt
	Metadata      map[string]any
	Metrics       map[string]float64
	Tags          map[string]string
	AgentManifest string
}

func (s *Span) Annotate(a SpanAnnotations) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		log.Warn("llmobs: cannot annotate a finished span")
		return
	}

	s.llmCtx.metadata = updateMapKeys(s.llmCtx.metadata, a.Metadata)
	s.llmCtx.metrics = updateMapKeys(s.llmCtx.metrics, a.Metrics)

	if len(a.Tags) > 0 {
		s.llmCtx.tags = updateMapKeys(s.llmCtx.tags, a.Tags)
		if sessionID, ok := a.Tags[TagKeySessionID]; ok {
			s.llmCtx.sessionID = sessionID
		}
	}

	if a.Prompt != nil {
		if s.llmCtx.spanKind != SpanKindLLM {
			log.Warn("llmobs: input prompt can only be annotated on llm spans, ignoring")
		} else {
			if a.Prompt.RAGContextVariables == nil {
				a.Prompt.RAGContextVariables = []string{"context"}
			}
			if a.Prompt.RAGQueryVariables == nil {
				a.Prompt.RAGQueryVariables = []string{"question"}
			}
			s.llmCtx.inputPrompt = a.Prompt
		}
	}

	if a.AgentManifest != "" {
		if s.llmCtx.spanKind != SpanKindAgent {
			log.Warn("llmobs: agent manifest can only be annotated on agent spans, ignoring")
		} else {
			s.llmCtx.agentManifest = a.AgentManifest
		}
	}

	s.annotateIO(a)
}

func (s *Span) annotateIO(a SpanAnnotations) {
	if a.OutputRetrievedDocs != nil && s.llmCtx.spanKind != SpanKindRetrieval {
		log.Warn("llmobs: retrieve docs can only be used to annotate outputs for retrieval spans, ignoring")
	}
	if a.InputEmbeddedDocs != nil && s.llmCtx.spanKind != SpanKindEmbedding {
		log.Warn("llmobs: embedding docs can only be used to annotate inputs for embedding spans, ignoring")
	}
	switch s.llmCtx.spanKind {
	case SpanKindLLM:
		s.annotateIOLLM(a)

	case SpanKindEmbedding:
		s.annotateIOEmbedding(a)

	case SpanKindRetrieval:
		s.annotateIORetrieval(a)

	case SpanKindExperiment:
		s.annotateIOExperiment(a)

	default:
		s.annotateIOText(a)
	}
}

func (s *Span) annotateIOLLM(a SpanAnnotations) {
	s.llmCtx.inputMessages = a.InputMessages
	s.llmCtx.outputMessages = a.OutputMessages
}

func (s *Span) annotateIOEmbedding(a SpanAnnotations) {
	if a.InputText != "" || a.InputMessages != nil {
		log.Warn("llmobs: embedding spans can only be annotated with input embedded docs, ignoring other inputs")
	}
	if a.OutputMessages != nil || a.OutputRetrievedDocs != nil {
		log.Warn("llmobs: embedding spans can only be annotated with output text, ignoring other outputs")
	}
	s.llmCtx.inputDocuments = a.InputEmbeddedDocs
	s.llmCtx.outputText = a.OutputText
}

func (s *Span) annotateIORetrieval(a SpanAnnotations) {
	if a.InputMessages != nil || a.InputEmbeddedDocs != nil {
		log.Warn("llmobs: retrieval spans can only be annotated with input text, ignoring other inputs")
	}
	if a.OutputText != "" || a.OutputMessages != nil {
		log.Warn("llmobs: retrieval spans can only be annotated with output retrieved docs, ignoring other outputs")
	}
	s.llmCtx.inputText = a.InputText
	s.llmCtx.outputDocuments = a.OutputRetrievedDocs
}

func (s *Span) annotateIOExperiment(a SpanAnnotations) {
	s.llmCtx.experimentInput = a.ExperimentInput
	s.llmCtx.experimentOutput = a.ExperimentOutput
	s.llmCtx.experimentExpectedOutput = a.ExperimentExpectedOutput
}

func (s *Span) annotateIOText(a SpanAnnotations) {
	if a.InputMessages != nil || a.InputEmbeddedDocs != nil {
		log.Warn("llmobs: %s spans can only be annotated with input text, ignoring other inputs", s.llmCtx.spanKind)
	}
	if a.OutputText != "" || a.OutputMessages != nil {
		log.Warn("llmobs: %s spans can only be annotated with output text, ignoring other outputs", s.llmCtx.spanKind)
	}
	s.llmCtx.inputText = a.InputText
	s.llmCtx.outputText = a.OutputText
}

// sessionID returns the session ID for a given span, by checking the span's nearest LLMObs span ancestor.
func (s *Span) sessionID() string {
	curSpan := s

	for curSpan != nil {
		if curSpan.llmCtx.sessionID != "" {
			return curSpan.llmCtx.sessionID
		}
		curSpan = curSpan.parent
	}
	return ""
}

// propagatedMLApp returns the ML App name for a given span, by checking the span's nearest LLMObs span ancestor.
// It defaults to the global config LLMObs ML App name.
func (s *Span) propagatedMLApp() string {
	curSpan := s

	for curSpan != nil {
		if curSpan.mlApp != "" {
			return curSpan.mlApp
		}
		curSpan = curSpan.parent
	}

	if s.propagated != nil && s.propagated.MLApp != "" {
		return s.propagated.MLApp
	}
	if activeLLMObs != nil {
		return activeLLMObs.Config.MLApp
	}
	return ""
}

// isEvaluationSpan returns whether the current span or any of the parents is an evaluation span.
func (s *Span) isEvaluationSpan() bool {
	curSpan := s
	for curSpan != nil {
		if curSpan.isEvaluation {
			return true
		}
		curSpan = curSpan.parent
	}
	return false
}

// updateMapKeys adds key/values from updates into src, overriding existing keys.
func updateMapKeys[K comparable, V any](src map[K]V, updates map[K]V) map[K]V {
	if len(updates) == 0 {
		return src
	}
	if src == nil {
		src = make(map[K]V, len(updates))
	}
	for k, v := range updates {
		src[k] = v
	}
	return src
}
