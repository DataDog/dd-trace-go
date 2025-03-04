package llmobs

import (
	"fmt"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// EvaluatorRunner processes span events and runs evaluators on them
type EvaluatorRunner struct {
	sync.Mutex
	queue         chan *EvaluationTask
	interval      time.Duration
	llmObs        *LLMObs
	stopCh        chan struct{}
	workerRunning bool
}

// EvaluationTask represents a task to evaluate a span
type EvaluationTask struct {
	SpanEvent *LLMObsSpanEvent
	Span      ddtrace.Span
}

// newEvaluatorRunner creates a new EvaluatorRunner
func newEvaluatorRunner(interval float64, llmObs *LLMObs) *EvaluatorRunner {
	return &EvaluatorRunner{
		queue:         make(chan *EvaluationTask, 1000),
		interval:      time.Duration(interval * float64(time.Second)),
		llmObs:        llmObs,
		stopCh:        make(chan struct{}),
		workerRunning: false,
	}
}

// start begins the evaluator runner worker
func (r *EvaluatorRunner) start() error {
	r.Lock()
	defer r.Unlock()

	if r.workerRunning {
		return fmt.Errorf("worker already running")
	}

	go r.worker()
	r.workerRunning = true
	return nil
}

// stop stops the evaluator runner worker
func (r *EvaluatorRunner) stop() error {
	r.Lock()
	defer r.Unlock()

	if !r.workerRunning {
		return fmt.Errorf("worker not running")
	}

	close(r.stopCh)
	r.workerRunning = false
	return nil
}

// enqueue adds an evaluation task to the queue
func (r *EvaluatorRunner) enqueue(spanEvent *LLMObsSpanEvent, span ddtrace.Span) {
	// Check if the span is suitable for evaluation
	if r.shouldEvaluate(spanEvent) {
		select {
		case r.queue <- &EvaluationTask{
			SpanEvent: spanEvent,
			Span:      span,
		}:
			// Successfully enqueued
		default:
			log.Debug("EvaluatorRunner queue full, dropping evaluation task")
		}
	}
}

// worker processes evaluation tasks from the queue
func (r *EvaluatorRunner) worker() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return

		case <-ticker.C:
			r.periodic()

		case task := <-r.queue:
			r.processTask(task)
		}
	}
}

// periodic flushes the evaluator runner queue
func (r *EvaluatorRunner) periodic() {
	// Drain the queue and process tasks
drainLoop:
	for {
		select {
		case task := <-r.queue:
			r.processTask(task)
		default:
			break drainLoop
		}
	}
}

// processTask processes an evaluation task
func (r *EvaluatorRunner) processTask(task *EvaluationTask) {
	// This is a placeholder implementation
	// In a real implementation, we would run evaluators on the span
	// and submit evaluation metrics

	// Example of a simple evaluator that checks for error status
	if task.SpanEvent.Status == "error" {
		// Create an evaluation metric
		metric := &LLMObsEvaluationMetric{
			JoinOn: map[string]interface{}{
				"span": map[string]string{
					"span_id":  task.SpanEvent.SpanID,
					"trace_id": task.SpanEvent.TraceID,
				},
			},
			Label:            "error_detected",
			MetricType:       "categorical",
			TimestampMs:      time.Now().UnixNano() / int64(time.Millisecond),
			CategoricalValue: "error",
			MLApp:            r.llmObs.mlApp,
			Tags: []string{
				"ddtrace.version:unknown",
				fmt.Sprintf("ml_app:%s", r.llmObs.mlApp),
			},
		}

		// Submit the evaluation metric
		r.llmObs.evalMetricWriter.enqueue(metric)
	}
}

// shouldEvaluate determines if a span should be evaluated
func (r *EvaluatorRunner) shouldEvaluate(spanEvent *LLMObsSpanEvent) bool {
	// This is a placeholder implementation
	// In a real implementation, we would check if the span is an LLM span
	// and has the necessary data for evaluation

	// For now, we'll assume all spans with kind "llm" should be evaluated
	if meta, ok := spanEvent.Meta.(map[string]interface{}); ok {
		if kind, ok := meta["span.kind"].(string); ok && kind == "llm" {
			return true
		}
	}

	return false
}

// SubmitEvaluationOptions contains options for submitting an evaluation metric
type SubmitEvaluationOptions struct {
	Label       string
	MetricType  string
	Value       interface{}
	SpanID      string
	TraceID     string
	TagKey      string
	TagValue    string
	Tags        map[string]string
	MLApp       string
	TimestampMs int64
	Metadata    map[string]interface{}
}

// SubmitEvaluation submits an evaluation metric for a span
func SubmitEvaluation(opts SubmitEvaluationOptions) error {
	llmObs := GetLLMObs()
	llmObs.RLock()
	defer llmObs.RUnlock()

	if !llmObs.enabled {
		log.Debug("SubmitEvaluation called when LLMObs is not enabled")
		return fmt.Errorf("LLMObs is not enabled")
	}

	// Validate required fields
	if opts.Label == "" {
		return fmt.Errorf("label is required")
	}

	if opts.MetricType == "" {
		return fmt.Errorf("metric type is required")
	}

	// Validate metric type
	metricType := opts.MetricType
	if metricType != "categorical" && metricType != "score" {
		return fmt.Errorf("metric type must be 'categorical' or 'score'")
	}

	// Validate value type based on metric type
	switch metricType {
	case "categorical":
		if _, ok := opts.Value.(string); !ok {
			return fmt.Errorf("value must be a string for categorical metrics")
		}
	case "score":
		if _, ok := opts.Value.(float64); !ok {
			if intVal, ok := opts.Value.(int); ok {
				// Convert int to float64
				opts.Value = float64(intVal)
			} else {
				return fmt.Errorf("value must be a number for score metrics")
			}
		}
	}

	// Determine join criteria (span ID/trace ID or tag key/value)
	if (opts.SpanID == "" || opts.TraceID == "") && (opts.TagKey == "" || opts.TagValue == "") {
		return fmt.Errorf("either span ID and trace ID or tag key and tag value must be provided")
	}

	// Create join_on field
	joinOn := make(map[string]interface{})
	if opts.SpanID != "" && opts.TraceID != "" {
		joinOn["span"] = map[string]string{
			"span_id":  opts.SpanID,
			"trace_id": opts.TraceID,
		}
	} else {
		joinOn["tag"] = map[string]string{
			"key":   opts.TagKey,
			"value": opts.TagValue,
		}
	}

	// Set timestamp
	timestampMs := opts.TimestampMs
	if timestampMs == 0 {
		timestampMs = time.Now().UnixNano() / int64(time.Millisecond)
	}

	// Set ML app
	mlApp := opts.MLApp
	if mlApp == "" {
		mlApp = llmObs.mlApp
	}

	// Create evaluation metric
	metric := &LLMObsEvaluationMetric{
		JoinOn:      joinOn,
		Label:       opts.Label,
		MetricType:  metricType,
		TimestampMs: timestampMs,
		MLApp:       mlApp,
		Tags: []string{
			"ddtrace.version:unknown",
			fmt.Sprintf("ml_app:%s", mlApp),
		},
	}

	// Set the appropriate value field
	switch metricType {
	case "categorical":
		metric.CategoricalValue = opts.Value.(string)
	case "score":
		metric.ScoreValue = opts.Value.(float64)
	}

	// Add tags
	if opts.Tags != nil {
		for k, v := range opts.Tags {
			metric.Tags = append(metric.Tags, fmt.Sprintf("%s:%s", k, v))
		}
	}

	// Add metadata
	if opts.Metadata != nil {
		metric.Metadata = opts.Metadata
	}

	// Submit the evaluation metric
	llmObs.evalMetricWriter.enqueue(metric)

	return nil
}

// SubmitEvaluationForExportedSpan submits an evaluation metric for a span using the exported span information
func SubmitEvaluationForExportedSpan(span *ExportedSpan, label string, metricType string, value interface{}, tags map[string]string, metadata map[string]interface{}) error {
	if span == nil {
		return fmt.Errorf("exported span cannot be nil")
	}

	return SubmitEvaluation(SubmitEvaluationOptions{
		Label:      label,
		MetricType: metricType,
		Value:      value,
		SpanID:     span.SpanID,
		TraceID:    span.TraceID,
		Tags:       tags,
		Metadata:   metadata,
	})
}
