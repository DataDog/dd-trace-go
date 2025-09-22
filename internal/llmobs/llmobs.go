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

var (
	errLLMObsNotEnabled        = errors.New("LLMObs is not enabled. Ensure the tracer has been started with the option tracer.WithLLMObsEnabled(true) or set DD_LLMOBS_ENABLED=true")
	errAgentlessRequiresAPIKey = errors.New("LLMOBs agentless mode requires a valid API key - set the DD_API_KEY env variable to configure one")
	errMLAppRequired           = errors.New("ML App is required for sending LLM Observability data")
)

const (
	baggageKeyExperimentID = "_ml_obs.experiment_id"
)

const (
	defaultParentID = "undefined"
)

type SpanKind string

const (
	SpanKindExperiment SpanKind = "experiment"
	SpanKindWorkflow   SpanKind = "workflow"
	SpanKindLLM        SpanKind = "llm"
	SpanKindEmbedding  SpanKind = "embedding"
	SpanKindAgent      SpanKind = "agent"
	SpanKindRetrieval  SpanKind = "retrieval"
	SpanKindTask       SpanKind = "task"
	SpanKindTool       SpanKind = "tool"
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

type llmobsContext struct {
	spanKind        SpanKind
	sessionID       string
	metadata        map[string]any
	metrics         map[string]float64
	tags            map[string]string
	agentManifest   string
	modelName       string
	modelProvider   string
	toolDefinitions string

	inputDocuments []EmbeddedDocument
	inputMessages  []LLMMessage
	inputText      string
	inputPrompt    *Prompt

	outputDocuments []RetrievedDocument
	outputMessages  []LLMMessage
	outputText      string

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

func Flush() {
	if activeLLMObs != nil {
		activeLLMObs.Flush()
	}
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
				log.Debug("llmobs: periodic flush signal")
				l.flushBuffers()

			// on-demand flush
			case <-l.flushNowCh:
				log.Debug("llmobs: explicit flush signal")
				l.flushBuffers()

			// shutdown: drain whatever's currently available, then final flush and exit
			case <-l.stopCh:
				log.Debug("llmobs: stop signal")

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

	// FIXME(rarguelloF): this blocks the reader goroutine
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
		} else {
			log.Debug("llmobs: LLMObsSpanSendEvents success")
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
	log.Debug("creating span event from llmobs context: %+v", span.llmCtx)

	meta := make(map[string]any)

	spanKind := span.llmCtx.spanKind
	meta["span.kind"] = string(spanKind)

	if (spanKind == SpanKindLLM || spanKind == SpanKindEmbedding) && span.llmCtx.modelName != "" || span.llmCtx.modelProvider != "" {
		modelName := span.llmCtx.modelName
		if modelName == "" {
			modelName = "custom"
		}
		modelProvider := strings.ToLower(span.llmCtx.modelProvider)
		if modelProvider == "" {
			modelProvider = "custom"
		}
		meta["model_name"] = modelName
		meta["model_provider"] = modelProvider
	}

	metadata := span.llmCtx.metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}
	if spanKind == SpanKindAgent && span.llmCtx.agentManifest != "" {
		metadata["agent_manifest"] = span.llmCtx.agentManifest
	}
	if len(metadata) > 0 {
		meta["metadata"] = metadata
	}

	input := make(map[string]any)
	output := make(map[string]any)

	if spanKind == SpanKindLLM && len(span.llmCtx.inputMessages) > 0 {
		input["messages"] = span.llmCtx.inputMessages
	} else if txt := span.llmCtx.inputText; len(txt) > 0 {
		input["value"] = txt
	}

	if spanKind == SpanKindLLM && len(span.llmCtx.outputMessages) > 0 {
		output["messages"] = span.llmCtx.outputMessages
	} else if txt := span.llmCtx.outputText; len(txt) > 0 {
		output["value"] = txt
	}

	if spanKind == SpanKindExperiment {
		if expectedOut := span.llmCtx.experimentExpectedOutput; expectedOut != nil {
			meta["expected_output"] = expectedOut
		}
		if expInput := span.llmCtx.experimentInput; expInput != nil {
			input = expInput
		}
		if out := span.llmCtx.experimentOutput; out != nil {
			// FIXME: experimentOutput is any, but in python is treated as a map
			meta["output"] = out
		}
	}

	if spanKind == SpanKindEmbedding {
		if inputDocs := span.llmCtx.inputDocuments; len(inputDocs) > 0 {
			input["documents"] = inputDocs
		}
	}
	if spanKind == SpanKindRetrieval {
		if outputDocs := span.llmCtx.outputDocuments; len(outputDocs) > 0 {
			output["documents"] = outputDocs
		}
	}
	if inputPrompt := span.llmCtx.inputPrompt; inputPrompt != nil {
		if spanKind != SpanKindLLM {
			log.Warn("llmobs: dropping prompt on non-LLM span kind, annotating prompts is only supported for LLM span kinds")
		} else {
			input["prompt"] = inputPrompt
		}
	} else if spanKind == SpanKindLLM {
		if span.parent != nil && span.parent.llmCtx.inputPrompt != nil {
			input["prompt"] = span.parent.llmCtx.inputPrompt
		}
	}

	if toolDefinitions := span.llmCtx.toolDefinitions; toolDefinitions != "" {
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

	spanID := span.apm.SpanID()
	parentID := defaultParentID
	if span.parent != nil {
		parentID = span.parent.apm.SpanID()
	}
	if span.llmTraceID == "" {
		log.Warn("llmobs: span has no trace ID")
		span.llmTraceID = newLLMObsTraceID()
	}

	tags := make(map[string]string)
	for k, v := range l.Config.TracerConfig.DDTags {
		tags[k] = fmt.Sprintf("%v", v)
	}
	tags["version"] = l.Config.TracerConfig.Version
	tags["env"] = l.Config.TracerConfig.Env
	tags["service"] = l.Config.TracerConfig.Service
	tags["source"] = "integration"
	tags["ml_app"] = span.mlApp
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

	for k, v := range span.llmCtx.tags {
		tags[k] = v
	}
	tagsSlice := make([]string, 0, len(tags))
	for k, v := range tags {
		tagsSlice = append(tagsSlice, fmt.Sprintf("%s:%s", k, v))
	}

	ev := &transport.LLMObsSpanEvent{
		SpanID:           spanID,
		TraceID:          span.llmTraceID,
		ParentID:         parentID,
		SessionID:        span.sessionID(),
		Tags:             tagsSlice,
		Name:             span.name,
		StartNS:          span.startTime.UnixNano(),
		Duration:         span.finishTime.Sub(span.startTime).Nanoseconds(),
		Status:           spanStatus,
		StatusMessage:    "",
		Meta:             meta,
		Metrics:          span.llmCtx.metrics,
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

func (l *LLMObs) StartSpan(ctx context.Context, kind SpanKind, name string, cfg StartSpanConfig) (*Span, context.Context) {
	spanName := name
	if spanName == "" {
		spanName = string(kind)
	}

	if cfg.StartTime.IsZero() {
		cfg.StartTime = time.Now()
	}

	startCfg := StartAPMSpanConfig{
		SpanType:  ext.SpanTypeLLM,
		StartTime: cfg.StartTime,
	}
	apmSpan, ctx := l.Tracer.StartSpan(ctx, name, startCfg)
	span := &Span{
		name:      spanName,
		apm:       apmSpan,
		startTime: cfg.StartTime,
	}
	if !l.Config.Enabled {
		log.Warn("llmobs: LLMObs span was started without enabling LLMObs")
		return span, ctx
	}

	if parent, ok := ActiveLLMSpanFromContext(ctx); ok {
		log.Debug("llmobs: found active llm span in context: %+v", parent)
		span.parent = parent
		span.llmTraceID = parent.llmTraceID
	} else if propagated, ok := PropagatedLLMSpanFromContext(ctx); ok {
		log.Debug("llmobs: found propagated llm span in context: %+v", propagated)
		span.propagated = propagated
		span.llmTraceID = propagated.TraceID
	} else {
		span.llmTraceID = newLLMObsTraceID()
	}

	span.mlApp = cfg.MLApp
	span.llmCtx = llmobsContext{
		spanKind:      kind,
		modelName:     cfg.ModelName,
		modelProvider: cfg.ModelProvider,
		sessionID:     cfg.SessionID,
	}

	if span.llmCtx.sessionID == "" {
		span.llmCtx.sessionID = span.sessionID()
	}
	if span.mlApp == "" {
		span.mlApp = span.propagatedMLApp()
		if span.mlApp == "" {
			// We should ensure there's always an ML App to fall back to during startup, so this should never happen.
			log.Warn("llmobs: ML App is required for sending LLM Observability data.")
		}
	}
	log.Debug("llmobs: starting LLMObs span: %s, span_kind: %s, ml_app: %s", name, kind, span.mlApp)
	return span, ContextWithActiveLLMSpan(ctx, span)
}

func (l *LLMObs) StartExperimentSpan(ctx context.Context, name string, experimentID string, cfg StartSpanConfig) (*Span, context.Context) {
	span, ctx := l.StartSpan(ctx, SpanKindExperiment, name, cfg)

	if experimentID != "" {
		span.apm.SetBaggageItem(baggageKeyExperimentID, experimentID)
		span.scope = "experiments"
	}
	return span, ctx
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
