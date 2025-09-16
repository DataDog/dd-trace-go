// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

var (
	mu           sync.Mutex
	activeLLMObs *LLMObs
)

type ctxKeyActiveLLMSpan struct{}

var (
	errLLMObsNotEnabled        = errors.New("LLMObs is not enabled. Ensure the experiment has been correctly initialized using experiment.New and llmobs.Start() has been called or set DD_LLMOBS_ENABLED=1")
	errAgentlessRequiresAPIKey = errors.New("LLMOBs agentless mode requires a valid API key - set the DD_API_KEY env variable to configure one")
	errMLAppRequired           = errors.New("ML App is required for sending LLM Observability data")
)

const (
	baggageKeyExperimentID = "_ml_obs.experiment_id"
)

const (
	defaultParentID = "undefined"
)

type OperationKind string

const (
	SpanKindExperiment = "experiment"
	SpanKindWorkflow   = "workflow"
	SpanKindLLM        = "llm"
	SpanKindEmbedding  = "embedding"
	SpanKindAgent      = "agent"
	SpanKindRetrieval  = "retrieval"
)

const (
	defaultFlushInterval   = 2 * time.Second
	defaultEvalDrainBudget = 500 * time.Millisecond
	defaultEvalPoolSize    = 4
)

const (
	sizeLimitEVPEvent        = 5_000_000 // 5MB
	collectionErrorDroppedIO = "dropped_io"
	droppedValueText         = "[This value has been dropped because this span's size exceeds the 1MB size limit.]"
)

// See: https://docs.datadoghq.com/getting_started/site/#access-the-datadog-site
var ddSitesNeedingAppSubdomain = []string{"datadoghq.com", "datadoghq.eu", "ddog-gov.com"}

type Document struct{}

type Message struct {
	Content string `json:"content"`
	Role    string `json:"role"`
}

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
	tags               map[string]string
	agentManifest      string
	modelName          string
	modelProvider      string
	toolDefinitions    string

	inputDocuments []Document
	inputMessages  []Message
	inputValue     string
	inputPrompt    string

	outputDocuments []Document
	outputMessages  []Message
	outputValue     string

	experimentInput          map[string]any
	experimentExpectedOutput any
	experimentOutput         any

	inputTokens  int64
	outputTokens int64
	totalTokens  int64
}

type spanEvalTask struct {
	event *transport.LLMObsSpanEvent
	span  *Span
}

type LLMObs struct {
	Config    config.Config
	Transport *transport.Transport
	Tracer    Tracer

	spanEventsCh   chan *transport.LLMObsSpanEvent
	spanEvalTaskCh chan *spanEvalTask
	evalMetricsCh  chan *transport.LLMObsEvaluationMetricEvent

	// runtime buffers
	bufSpanEvents  []*transport.LLMObsSpanEvent
	bufEvalMetrics []*transport.LLMObsEvaluationMetricEvent

	// lifecycle
	mu            sync.Mutex
	running       bool
	wg            sync.WaitGroup
	stopCh        chan struct{} // signal stop
	doneCh        chan struct{} // closed when run exits
	flushNowCh    chan struct{}
	flushInterval time.Duration

	// eval worker pool
	evalPoolSize    int
	evalWg          sync.WaitGroup
	evalStopCh      chan struct{} // closed to stop eval workers
	evalDrainBudget time.Duration
}

func newLLMObs(cfg config.Config, tracer Tracer) (*LLMObs, error) {
	var agentless bool
	if cfg.AgentlessEnabled != nil {
		agentless = *cfg.AgentlessEnabled
	} else {
		// if agentlessEnabled is not set and evp_proxy is supported in the agent, default to use the agent
		agentless = !cfg.AgentFeatures.EVPProxyV2
	}

	if agentless && !isAPIKeyValid(cfg.TracerConfig.APIKey) {
		return nil, errAgentlessRequiresAPIKey
	}
	if cfg.MLApp == "" {
		return nil, errMLAppRequired
	}
	if cfg.TracerConfig.HTTPClient == nil {
		cfg.TracerConfig.HTTPClient = cfg.DefaultHTTPClient(agentless)
	}
	return &LLMObs{
		Config:          cfg,
		Transport:       transport.New(cfg, agentless),
		Tracer:          tracer,
		spanEventsCh:    make(chan *transport.LLMObsSpanEvent),
		spanEvalTaskCh:  make(chan *spanEvalTask),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
		flushNowCh:      make(chan struct{}, 1), // small buffer so Flush() never blocks
		flushInterval:   defaultFlushInterval,
		evalPoolSize:    defaultEvalPoolSize,
		evalStopCh:      make(chan struct{}),
		evalDrainBudget: defaultEvalDrainBudget,
	}, nil
}

// Start starts the global LLMObs instance.
func Start(cfg config.Config, tracer Tracer) error {
	mu.Lock()
	defer mu.Unlock()

	if activeLLMObs != nil {
		activeLLMObs.Stop()
	}
	if !cfg.Enabled {
		return nil
	}
	l, err := newLLMObs(cfg, tracer)
	if err != nil {
		return err
	}
	activeLLMObs = l
	activeLLMObs.Run()
	return nil
}

// Stop stops the active LLMObs instance.
func Stop() {
	mu.Lock()
	defer mu.Unlock()

	if activeLLMObs != nil {
		activeLLMObs.Stop()
		activeLLMObs = nil
	}
}

// ActiveLLMObs returns the current active llmobs instance, or an error if there isn't one.
func ActiveLLMObs() (*LLMObs, error) {
	if activeLLMObs == nil || !activeLLMObs.Config.Enabled {
		return nil, errLLMObsNotEnabled
	}
	return activeLLMObs, nil
}

// Run starts the worker loop.
func (l *LLMObs) Run() {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.mu.Unlock()

	// Start eval worker pool
	l.startEvalWorkers()

	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer close(l.doneCh)

		ticker := time.NewTicker(l.flushInterval)
		defer ticker.Stop()

		for {
			select {
			// ingest (append to in-memory buffers)
			case ev := <-l.spanEventsCh:
				l.bufSpanEvents = append(l.bufSpanEvents, ev)

			case evalMetric := <-l.evalMetricsCh:
				l.bufEvalMetrics = append(l.bufEvalMetrics, evalMetric)

			// periodic flush
			case <-ticker.C:
				l.flushBuffers()

			// on-demand flush
			case <-l.flushNowCh:
				l.flushBuffers()

			// shutdown: drain whatever's currently available, then final flush and exit
			case <-l.stopCh:
				l.drainNonBlocking()
				l.flushBuffers()
				return
			}
		}
	}()
}

// Flush forces an immediate flush of anything currently buffered.
// It does not wait for new items to arrive.
func (l *LLMObs) Flush() {
	l.Tracer.Flush()
	// non-blocking edge trigger so multiple calls coalesce
	select {
	case l.flushNowCh <- struct{}{}:
	default:
	}
}

// Stop requests shutdown, drains what’s already in the channels, flushes, and waits.
func (l *LLMObs) Stop() {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return
	}
	l.running = false

	// 1) Stop eval workers first
	select {
	case <-l.evalStopCh:
		// already closed
	default:
		close(l.evalStopCh)
	}
	l.mu.Unlock()

	// Wait for workers to finish any task they're currently running
	l.evalWg.Wait()

	// Best-effort drain any remaining tasks from the input channel (we don't own it)
	l.drainEvalTasksWithBudget(l.evalDrainBudget)

	// 3) Now stop the sender/flush loop
	select {
	case <-l.stopCh:
	default:
		close(l.stopCh)
	}

	// 4) Wait for the main loop to exit (it will do a final flush)
	l.wg.Wait()
}

func (l *LLMObs) startEvalWorkers() {
	size := l.evalPoolSize
	if size <= 0 {
		size = 1
	}
	for i := 0; i < size; i++ {
		l.evalWg.Add(1)
		go func() {
			defer l.evalWg.Done()
			for {
				select {
				case <-l.evalStopCh:
					return
				case task := <-l.spanEvalTaskCh:
					l.runEvalTask(task)
				}
			}
		}()
	}
}

// drainNonBlocking pulls everything currently buffered in the channels into our in-memory buffers.
func (l *LLMObs) drainNonBlocking() {
	for {
		progress := false
		select {
		case ev := <-l.spanEventsCh:
			l.bufSpanEvents = append(l.bufSpanEvents, ev)
			progress = true
		default:
		}

		select {
		case evalMetric := <-l.evalMetricsCh:
			l.bufEvalMetrics = append(l.bufEvalMetrics, evalMetric)
			progress = true
		default:
		}

		if !progress {
			return
		}
	}
}

// drainEvalTasksWithBudget pulls tasks for a short period and runs them inline.
// Called only after eval workers are stopped.
func (l *LLMObs) drainEvalTasksWithBudget(budget time.Duration) {
	if budget <= 0 {
		return
	}
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		drained := false
		select {
		case task := <-l.spanEvalTaskCh:
			if task != nil {
				l.runEvalTask(task)
			}
			drained = true
		default:
		}
		if !drained {
			time.Sleep(1 * time.Millisecond)
		}
	}
}

// flushBuffers processes and clears the in-memory buffers.
// Replace the bodies with your real batching / API calls.
func (l *LLMObs) flushBuffers() {
	// fast-path: nothing to do
	if len(l.bufSpanEvents) == 0 && len(l.bufEvalMetrics) == 0 {
		return
	}

	// snapshot + clear buffers so producers can keep appending while we process
	events := l.bufSpanEvents
	evalMetrics := l.bufEvalMetrics
	l.bufSpanEvents = nil
	l.bufEvalMetrics = nil

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if len(events) > 0 {
		log.Debug("llmobs: sending %d LLMObs Span Events", len(events))
		if log.DebugEnabled() {
			for _, ev := range events {
				if b, err := json.Marshal(ev); err == nil {
					log.Debug("llmobs: LLMObs Span Event: %s", b)
				}
			}
		}
		if err := l.Transport.LLMObsSpanSendEvents(ctx, events); err != nil {
			log.Error("llmobs: LLMObsSpanSendEvents failed: %v", err)
		}
	}

	if len(evalMetrics) > 0 {
		log.Debug("llmobs: sending %d LLMObs Span Eval Metrics", len(evalMetrics))
		if log.DebugEnabled() {
			for _, eval := range evalMetrics {
				if b, err := json.Marshal(eval); err == nil {
					log.Debug("llmobs: LLMObs Span Eval Metric: %s", b)
				}
			}
		}
		if err := l.Transport.LLMObsEvalMetricsSend(ctx, evalMetrics); err != nil {
			log.Error("llmobs: LLMObsEvalMetricsSend failed: %v", err)
		}
	}
}

func (l *LLMObs) runEvalTask(task *spanEvalTask) {
	log.Debug("llmobs: runEvalTask not implemented yet")
}

type Span struct {
	mu sync.RWMutex

	apm           APMSpan
	parent        *Span
	llmobsCtx     llmobsContext
	llmobsTraceID string
	name          string
	integration   string
	scope         string
	isEvaluation  bool
	error         error
	startTime     time.Time
	finishTime    time.Time
	spanLinks     []SpanLink
}

func (s *Span) AddLink(link SpanLink) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.apm.AddLink(link)
	s.spanLinks = append(s.spanLinks, link)
}

func (s *Span) SpanID() string {
	return s.apm.SpanID()
}

func (s *Span) TraceID() string {
	return s.apm.TraceID()
}

func (s *Span) Finish(opts ...FinishSpanOption) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := &finishSpanConfig{
		finishTime: time.Now(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	s.finishTime = cfg.finishTime
	apmFinishCfg := FinishAPMSpanConfig{
		FinishTime: cfg.finishTime,
	}
	if cfg.error != nil {
		s.error = cfg.error
		apmFinishCfg.Error = cfg.error
	}

	s.apm.Finish(apmFinishCfg)
	l, err := ActiveLLMObs()
	if err != nil {
		return
	}
	l.submitLLMObsSpan(s)

	//TODO: telemetry.record_span_created(span)
}

// submitLLMObsSpan generates and submits an LLMObs span event to the LLMObs intake.
func (l *LLMObs) submitLLMObsSpan(span *Span) {
	event := l.llmobsSpanEvent(span)

	l.spanEventsCh <- event
	if !(span.isEvaluationSpan()) {
		l.spanEvalTaskCh <- &spanEvalTask{
			event: event,
			span:  span,
		}
	}
}

func (l *LLMObs) llmobsSpanEvent(span *Span) *transport.LLMObsSpanEvent {
	meta := make(map[string]any)

	spanKind := span.llmobsCtx.spanKind
	meta["span.kind"] = spanKind

	if (spanKind == SpanKindLLM || spanKind == SpanKindEmbedding) && span.llmobsCtx.modelName != "" {
		meta["model_name"] = span.llmobsCtx.modelName
		modelProvider := strings.ToLower(span.llmobsCtx.modelProvider)
		if modelProvider == "" {
			modelProvider = "custom"
		}
	}

	metadata := span.llmobsCtx.metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}
	if spanKind == SpanKindAgent && span.llmobsCtx.agentManifest != "" {
		metadata["agent_manifest"] = span.llmobsCtx.agentManifest
	}
	if len(metadata) > 0 {
		meta["metadata"] = metadata
	}

	input := make(map[string]any)
	output := make(map[string]any)

	if spanKind == SpanKindLLM && len(span.llmobsCtx.inputMessages) > 0 {
		input["messages"] = span.llmobsCtx.inputMessages
	} else if inputValue := span.llmobsCtx.inputValue; inputValue != "" {
		input["value"] = inputValue
	}

	if spanKind == SpanKindLLM && len(span.llmobsCtx.outputMessages) > 0 {
		output["messages"] = span.llmobsCtx.outputMessages
	} else if outputValue := span.llmobsCtx.outputValue; outputValue != "" {
		output["value"] = outputValue
	}

	if spanKind == SpanKindExperiment {
		if expectedOut := span.llmobsCtx.experimentExpectedOutput; expectedOut != nil {
			meta["expected_output"] = expectedOut
		}
		if expInput := span.llmobsCtx.experimentInput; expInput != nil {
			input = expInput
		}
		if out := span.llmobsCtx.experimentOutput; out != nil {
			// FIXME: experimentOutput is any, but in python is treated as a map
			meta["output"] = out
		}
	}

	if spanKind == SpanKindEmbedding {
		if inputDocs := span.llmobsCtx.inputDocuments; len(inputDocs) > 0 {
			input["documents"] = inputDocs
		}
	}
	if spanKind == SpanKindRetrieval {
		if outputDocs := span.llmobsCtx.outputDocuments; len(outputDocs) > 0 {
			output["documents"] = outputDocs
		}
	}
	if inputPrompt := span.llmobsCtx.inputPrompt; inputPrompt != "" {
		if spanKind != SpanKindLLM {
			log.Warn("llmobs: dropping prompt on non-LLM span kind, annotating prmpts is only supported for LLM span kinds")
		} else {
			input["prompt"] = inputPrompt
		}
	} else if spanKind == SpanKindLLM {
		if span.parent != nil && span.parent.llmobsCtx.inputPrompt != "" {
			input["prompt"] = span.parent.llmobsCtx.inputPrompt
		}
	}

	if toolDefinitions := span.llmobsCtx.toolDefinitions; toolDefinitions != "" {
		meta["tool_definitions"] = toolDefinitions
	}

	spanStatus := "ok"
	var errMsg *transport.ErrorMessage
	if span.error != nil {
		spanStatus = "error"
		errMsg = transport.NewErrorMessage(span.error)
		meta["error.message"] = errMsg.Message
		meta["error.stack"] = errMsg.Stack
		meta["error.type"] = errMsg.Type
	}

	if len(input) > 0 {
		meta["input"] = input
	}
	if len(output) > 0 {
		meta["output"] = output
	}

	// TODO(rarguelloF): user span processor

	spanID := span.apm.SpanID()
	parentID := defaultParentID
	if span.parent != nil {
		parentID = span.parent.apm.SpanID()
	}
	if span.llmobsTraceID == "" {
		log.Warn("llmobs: span has no trace ID")
		span.llmobsTraceID = newLLMObsTraceID()
	}

	metrics := make(map[string]any)
	if span.llmobsCtx.inputTokens > 0 {
		metrics["input_tokens"] = span.llmobsCtx.inputTokens
	}
	if span.llmobsCtx.outputTokens > 0 {
		metrics["output_tokens"] = span.llmobsCtx.outputTokens
	}
	if span.llmobsCtx.totalTokens > 0 {
		metrics["total_tokens"] = span.llmobsCtx.totalTokens
	}

	tags := make(map[string]string)
	for k, v := range l.Config.TracerConfig.DDTags {
		tags[k] = fmt.Sprintf("%v", v)
	}
	tags["version"] = l.Config.TracerConfig.Version
	tags["env"] = l.Config.TracerConfig.Env
	tags["service"] = l.Config.TracerConfig.Service
	tags["source"] = "integration" // TODO(rarguelloF): is this correct?
	tags["ml_app"] = span.llmobsCtx.mlApp
	tags["ddtrace.version"] = version.Tag
	tags["language"] = "go"

	errTag := "0"
	if span.error != nil {
		errTag = "1"
	}
	tags["error"] = errTag

	if errMsg != nil {
		tags["error_type"] = errMsg.Type
	}
	if span.integration != "" {
		tags["integration"] = span.integration
	}
	// TODO(rarguelloF)
	//if _is_evaluation_span(span):
	//tags[constants.RUNNER_IS_INTEGRATION_SPAN_TAG] = "ragas"

	for k, v := range span.llmobsCtx.tags {
		tags[k] = v
	}
	tagsSlice := make([]string, 0, len(tags))
	for k, v := range tags {
		tagsSlice = append(tagsSlice, fmt.Sprintf("%s:%s", k, v))
	}

	ev := &transport.LLMObsSpanEvent{
		SpanID:           spanID,
		TraceID:          span.llmobsTraceID,
		ParentID:         parentID,
		SessionID:        span.sessionID(),
		Tags:             tagsSlice,
		Name:             span.name,
		StartNS:          span.startTime.UnixNano(),
		Duration:         span.finishTime.Sub(span.startTime).Nanoseconds(),
		Status:           spanStatus,
		StatusMessage:    "",
		Meta:             meta,
		Metrics:          metrics,
		CollectionErrors: nil,
		SpanLinks:        span.spanLinks,
		Scope:            span.scope,
	}
	if b, err := json.Marshal(ev); err == nil {
		if len(b) > sizeLimitEVPEvent {
			log.Warn(
				"llmobs: dropping llmobs span event input/output because its size (%s) exceeds the event size limit (5MB)",
				readableBytes(len(b)),
			)
			truncateLLMObsSpanEvent(ev, input, output)
		}
	}
	return ev
}

func truncateLLMObsSpanEvent(ev *transport.LLMObsSpanEvent, input, output map[string]any) {
	if _, ok := input["value"]; ok {
		input["value"] = droppedValueText
	}
	ev.Meta["input"] = input

	if _, ok := output["value"]; ok {
		output["value"] = droppedValueText
	}
	ev.Meta["output"] = output

	ev.CollectionErrors = []string{collectionErrorDroppedIO}
}

func readableBytes(s int) string {
	const base = 1000
	sizes := []string{"B", "kB", "MB", "GB", "TB", "PB", "EB"}

	if s < 10 {
		return fmt.Sprintf("%dB", s)
	}
	e := math.Floor(logn(float64(s), base))
	suffix := sizes[int(e)]
	val := math.Floor(float64(s)/math.Pow(base, e)*10+0.5) / 10
	f := "%.0f%s"
	if val < 10 {
		f = "%.1f%s"
	}
	return fmt.Sprintf(f, val, suffix)
}

func logn(n, b float64) float64 {
	return math.Log(n) / math.Log(b)
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
		startTime:     time.Now(),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.mlApp == "" {
		cfg.mlApp = l.Config.MLApp
	}

	startCfg := StartAPMSpanConfig{
		SpanType:  ext.SpanTypeLLM,
		StartTime: cfg.startTime,
	}
	apmSpan, ctx := l.Tracer.StartSpan(ctx, name, startCfg)
	span := &Span{
		name:      spanName,
		apm:       apmSpan,
		startTime: cfg.startTime,
	}
	if !l.Config.Enabled {
		log.Warn("llmobs: LLMObs span was started without enabling LLMObs")
		return span, ctx
	}

	parent, ok := getActiveLLMSpan(ctx)
	if ok {
		log.Debug("llmobs: found active llm span in context: %v", parent)
		span.parent = parent
		span.llmobsTraceID = parent.llmobsTraceID
	} else {
		span.llmobsTraceID = newLLMObsTraceID()
	}

	span.llmobsCtx = llmobsContext{
		spanKind:      string(opKind),
		modelName:     cfg.modelName,
		modelProvider: cfg.modelProvider,
		sessionID:     cfg.sessionID,
		mlApp:         cfg.mlApp,
	}

	if span.llmobsCtx.sessionID == "" {
		span.llmobsCtx.sessionID = span.sessionID()
	}
	if span.llmobsCtx.mlApp == "" {
		mlApp := span.mlApp(ctx)
		if mlApp == "" {
			// We should ensure there's always an ML App to fall back to during startup, so this should never happen.
			log.Warn("llmobs: ML App is required for sending LLM Observability data.")
		} else {
			span.llmobsCtx.mlApp = mlApp
		}
	}
	log.Debug("llmobs: starting LLMObs span: %s, span_kind: %s, ml_app: %s", name, opKind, span.llmobsCtx.mlApp)
	return span, setActiveLLMSpan(ctx, span)
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
	span.mu.Lock()
	defer span.mu.Unlock()

	//TODO(rarguelloF): complete
}

type ExperimentSpanAnnotations struct {
	Input          map[string]any
	Output         any
	ExpectedOutput any
	Tags           map[string]string
}

func (l *LLMObs) AnnotateExperimentSpan(span *Span, annotations ExperimentSpanAnnotations) {
	span.mu.Lock()
	defer span.mu.Unlock()

	span.llmobsCtx.experimentInput = annotations.Input
	span.llmobsCtx.experimentOutput = annotations.Output
	span.llmobsCtx.experimentExpectedOutput = annotations.ExpectedOutput
	span.llmobsCtx.tags = annotations.Tags
}

func (l *LLMObs) StartExperimentSpan(ctx context.Context, name string, experimentID string, opts ...StartSpanOption) (*Span, context.Context) {
	span, ctx := l.StartSpan(ctx, SpanKindExperiment, name, opts...)

	if experimentID != "" {
		span.apm.SetBaggageItem(baggageKeyExperimentID, experimentID)
		span.scope = "experiments"
	}
	return span, ctx
}

// sessionID returns the session ID for a given span, by checking the span's nearest LLMObs span ancestor.
func (s *Span) sessionID() string {
	curSpan := s

	for curSpan != nil {
		if curSpan.llmobsCtx.sessionID != "" {
			return curSpan.llmobsCtx.sessionID
		}
		curSpan = curSpan.parent
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

// mlApp returns the ML App name for a given span, by checking the span's nearest LLMObs span ancestor.
// It defaults to the global config LLMObs ML App name.
func (s *Span) mlApp(ctx context.Context) string {
	curSpan := s

	for curSpan != nil {
		if curSpan.llmobsCtx.mlApp != "" {
			return curSpan.llmobsCtx.mlApp
		}
		curSpan = curSpan.parent
	}

	if propagated, ok := PropagatedMLAppFromContext(ctx); ok {
		return propagated
	}
	if activeLLMObs != nil {
		return activeLLMObs.Config.MLApp
	}
	return ""
}

func setActiveLLMSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, ctxKeyActiveLLMSpan{}, span)
}

func getActiveLLMSpan(ctx context.Context) (*Span, bool) {
	if span, ok := ctx.Value(ctxKeyActiveLLMSpan{}).(*Span); ok {
		return span, true
	}
	return nil, false
}

func newLLMObsTraceID() string {
	var b [16]byte

	// High 32 bits: Unix seconds
	secs := uint32(time.Now().Unix())
	binary.BigEndian.PutUint32(b[0:4], secs)

	// Middle 32 bits: zero
	// (already zeroed by array initialization)

	// Low 64 bits: random
	if _, err := rand.Read(b[8:16]); err != nil {
		panic(err)
	}

	// Turn into a big.Int
	x := new(big.Int).SetBytes(b[:])

	// 32-byte hex string
	return fmt.Sprintf("%032x", x)
}

// PublicResourceBaseURL returns the base URL to access a resource (experiments, projects, etc.)
func PublicResourceBaseURL() string {
	site := "datadoghq.com"
	if activeLLMObs != nil {
		site = activeLLMObs.Config.TracerConfig.Site
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
