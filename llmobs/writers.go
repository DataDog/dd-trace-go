package llmobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// LLMObsSpanEvent represents a span event to be sent to the LLMObs backend
type LLMObsSpanEvent struct {
	TraceID   string                   `json:"trace_id"`
	SpanID    string                   `json:"span_id"`
	ParentID  string                   `json:"parent_id"`
	Name      string                   `json:"name"`
	StartNs   int64                    `json:"start_ns"`
	Duration  int64                    `json:"duration"`
	Status    string                   `json:"status"`
	Meta      map[string]interface{}   `json:"meta"`
	Metrics   map[string]float64       `json:"metrics"`
	Tags      []string                 `json:"tags"`
	SessionID string                   `json:"session_id,omitempty"`
	SpanLinks []map[string]interface{} `json:"span_links,omitempty"`
	DD        map[string]string        `json:"_dd"`
}

// LLMObsEvaluationMetric represents an evaluation metric to be sent to the LLMObs backend
type LLMObsEvaluationMetric struct {
	JoinOn           map[string]interface{} `json:"join_on"`
	Label            string                 `json:"label"`
	MetricType       string                 `json:"metric_type"`
	TimestampMs      int64                  `json:"timestamp_ms"`
	ScoreValue       float64                `json:"score_value,omitempty"`
	CategoricalValue string                 `json:"categorical_value,omitempty"`
	MLApp            string                 `json:"ml_app"`
	Tags             []string               `json:"tags"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

// LLMObsSpanWriter handles sending span events to the LLMObs backend
type LLMObsSpanWriter struct {
	sync.Mutex
	queue            chan *LLMObsSpanEvent
	isAgentless      bool
	agentlessURL     string
	interval         time.Duration
	timeout          time.Duration
	httpClient       *http.Client
	stopCh           chan struct{}
	flushCompleteCh  chan struct{}
	flushRequestedCh chan chan struct{}
	workerRunning    bool
}

// LLMObsEvalMetricWriter handles sending evaluation metrics to the LLMObs backend
type LLMObsEvalMetricWriter struct {
	sync.Mutex
	queue            chan *LLMObsEvaluationMetric
	site             string
	apiKey           string
	interval         time.Duration
	timeout          time.Duration
	httpClient       *http.Client
	stopCh           chan struct{}
	flushCompleteCh  chan struct{}
	flushRequestedCh chan chan struct{}
	workerRunning    bool
}

// newLLMObsSpanWriter creates a new LLMObsSpanWriter
func newLLMObsSpanWriter(isAgentless bool, agentlessURL string, interval, timeout float64) *LLMObsSpanWriter {
	return &LLMObsSpanWriter{
		queue:            make(chan *LLMObsSpanEvent, 1000),
		isAgentless:      isAgentless,
		agentlessURL:     agentlessURL,
		interval:         time.Duration(interval * float64(time.Second)),
		timeout:          time.Duration(timeout * float64(time.Second)),
		httpClient:       &http.Client{Timeout: time.Duration(timeout * float64(time.Second))},
		stopCh:           make(chan struct{}),
		flushCompleteCh:  make(chan struct{}),
		flushRequestedCh: make(chan chan struct{}),
		workerRunning:    false,
	}
}

// newLLMObsEvalMetricWriter creates a new LLMObsEvalMetricWriter
func newLLMObsEvalMetricWriter(site, apiKey string, interval, timeout float64) *LLMObsEvalMetricWriter {
	return &LLMObsEvalMetricWriter{
		queue:            make(chan *LLMObsEvaluationMetric, 1000),
		site:             site,
		apiKey:           apiKey,
		interval:         time.Duration(interval * float64(time.Second)),
		timeout:          time.Duration(timeout * float64(time.Second)),
		httpClient:       &http.Client{Timeout: time.Duration(timeout * float64(time.Second))},
		stopCh:           make(chan struct{}),
		flushCompleteCh:  make(chan struct{}),
		flushRequestedCh: make(chan chan struct{}),
		workerRunning:    false,
	}
}

// start begins the span writer worker
func (w *LLMObsSpanWriter) start() error {
	w.Lock()
	defer w.Unlock()

	if w.workerRunning {
		return fmt.Errorf("worker already running")
	}

	go w.worker()
	w.workerRunning = true
	return nil
}

// start begins the evaluation metric writer worker
func (w *LLMObsEvalMetricWriter) start() error {
	w.Lock()
	defer w.Unlock()

	if w.workerRunning {
		return fmt.Errorf("worker already running")
	}

	go w.worker()
	w.workerRunning = true
	return nil
}

// stop stops the span writer worker
func (w *LLMObsSpanWriter) stop() error {
	w.Lock()
	defer w.Unlock()

	if !w.workerRunning {
		return fmt.Errorf("worker not running")
	}

	close(w.stopCh)
	<-w.flushCompleteCh
	w.workerRunning = false
	return nil
}

// stop stops the evaluation metric writer worker
func (w *LLMObsEvalMetricWriter) stop() error {
	w.Lock()
	defer w.Unlock()

	if !w.workerRunning {
		return fmt.Errorf("worker not running")
	}

	close(w.stopCh)
	<-w.flushCompleteCh
	w.workerRunning = false
	return nil
}

// enqueue adds a span event to the queue
func (w *LLMObsSpanWriter) enqueue(event *LLMObsSpanEvent) {
	select {
	case w.queue <- event:
		// Successfully enqueued
	default:
		log.Debug("LLMObsSpanWriter queue full, dropping span event")
	}
}

// enqueue adds an evaluation metric to the queue
func (w *LLMObsEvalMetricWriter) enqueue(metric *LLMObsEvaluationMetric) {
	select {
	case w.queue <- metric:
		// Successfully enqueued
	default:
		log.Debug("LLMObsEvalMetricWriter queue full, dropping evaluation metric")
	}
}

// worker processes span events from the queue
func (w *LLMObsSpanWriter) worker() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	defer close(w.flushCompleteCh)

	var batch []*LLMObsSpanEvent

	drain := func() {
	drainLoop:
		for {
			select {
			case event := <-w.queue:
				batch = append(batch, event)
			default:
				break drainLoop
			}
		}
	}

	flush := func() {
		if len(batch) == 0 {
			return
		}

		if err := w.flush(batch); err != nil {
			log.Debug("Error flushing LLMObs span events: %v", err)
		}

		// Clear the batch
		batch = batch[:0]
	}

	for {
		select {
		case <-w.stopCh:
			drain()
			flush()
			return

		case <-ticker.C:
			drain()
			flush()

		case event := <-w.queue:
			batch = append(batch, event)
			if len(batch) >= 100 {
				flush()
			}

		case doneCh := <-w.flushRequestedCh:
			drain()
			flush()
			close(doneCh)
		}
	}
}

// worker processes evaluation metrics from the queue
func (w *LLMObsEvalMetricWriter) worker() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	defer close(w.flushCompleteCh)

	var batch []*LLMObsEvaluationMetric

	drain := func() {
	drainLoop:
		for {
			select {
			case metric := <-w.queue:
				batch = append(batch, metric)
			default:
				break drainLoop
			}
		}
	}

	flush := func() {
		if len(batch) == 0 {
			return
		}

		if err := w.flush(batch); err != nil {
			log.Debug("Error flushing LLMObs evaluation metrics: %v", err)
		}

		// Clear the batch
		batch = batch[:0]
	}

	for {
		select {
		case <-w.stopCh:
			drain()
			flush()
			return

		case <-ticker.C:
			drain()
			flush()

		case metric := <-w.queue:
			batch = append(batch, metric)
			if len(batch) >= 100 {
				flush()
			}

		case doneCh := <-w.flushRequestedCh:
			drain()
			flush()
			close(doneCh)
		}
	}
}

// flush sends a batch of span events to the LLMObs backend
func (w *LLMObsSpanWriter) flush(batch []*LLMObsSpanEvent) error {
	if len(batch) == 0 {
		return nil
	}

	// Serialize the batch
	payload, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("error marshaling span events: %w", err)
	}

	// Determine the endpoint based on agentless configuration
	var url string
	if w.isAgentless {
		url = w.agentlessURL
	} else {
		// Use the agent endpoint
		url = "http://localhost:8126/v0.1/llmobs/spans"
	}

	// Create the request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	if w.isAgentless {
		// Add API key header for agentless mode
		apiKey := GetLLMObs().apiKey
		if apiKey != "" {
			req.Header.Set("DD-API-KEY", apiKey)
		}
	}

	// Send the request
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("received non-success status code: %d", resp.StatusCode)
	}

	return nil
}

// flush sends a batch of evaluation metrics to the LLMObs backend
func (w *LLMObsEvalMetricWriter) flush(batch []*LLMObsEvaluationMetric) error {
	if len(batch) == 0 {
		return nil
	}

	// Serialize the batch
	payload, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("error marshaling evaluation metrics: %w", err)
	}

	// Construct the URL
	url := fmt.Sprintf("https://api.%s/api/v2/llmobs/evaluations", w.site)

	// Create the request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DD-API-KEY", w.apiKey)

	// Send the request
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("received non-success status code: %d", resp.StatusCode)
	}

	return nil
}

// periodic flushes the span writer queue
func (w *LLMObsSpanWriter) periodic() {
	w.Lock()
	defer w.Unlock()

	if !w.workerRunning {
		return
	}

	doneCh := make(chan struct{})
	w.flushRequestedCh <- doneCh
	<-doneCh
}

// periodic flushes the evaluation metric writer queue
func (w *LLMObsEvalMetricWriter) periodic() {
	w.Lock()
	defer w.Unlock()

	if !w.workerRunning {
		return
	}

	doneCh := make(chan struct{})
	w.flushRequestedCh <- doneCh
	<-doneCh
}

// createLLMObsSpanEvent creates a span event from a ddtrace.Span
func createLLMObsSpanEvent(span ddtrace.Span) *LLMObsSpanEvent {
	// This is a placeholder implementation
	// In a real implementation, we would extract all the necessary data from the span
	// including custom tags for LLM data

	// For now, we'll just create a basic event with minimal data
	event := &LLMObsSpanEvent{
		TraceID:  fmt.Sprintf("%d", span.Context().TraceID()),
		SpanID:   fmt.Sprintf("%d", span.Context().SpanID()),
		ParentID: rootParentID,                               // Default to root
		Name:     "unknown",                                  // Would extract from span
		StartNs:  time.Now().UnixNano() - int64(time.Second), // Approximation
		Duration: int64(time.Second),                         // Approximation
		Status:   "ok",
		Meta:     make(map[string]interface{}),
		Metrics:  make(map[string]float64),
		Tags:     []string{},
		DD: map[string]string{
			"span_id":  fmt.Sprintf("%d", span.Context().SpanID()),
			"trace_id": fmt.Sprintf("%d", span.Context().TraceID()),
		},
	}

	// In a real implementation, we would extract error information
	if errorTag, exists := getTagValue(span, ext.Error); exists && errorTag == "true" {
		event.Status = "error"
	}

	return event
}

// onSpanFinish is called when a span is finished
func onSpanFinish(span ddtrace.Span) {
	llmObs := GetLLMObs()
	llmObs.RLock()
	defer llmObs.RUnlock()

	if !llmObs.enabled {
		return
	}

	// Check if this is an LLM span
	spanTypeGetter, ok := span.(interface{ SpanType() string })
	if !ok || spanTypeGetter.SpanType() != SpanTypeLLM {
		return
	}

	// Create and submit the span event
	event := createLLMObsSpanEvent(span)
	if event != nil {
		llmObs.spanWriter.enqueue(event)
	}
}
