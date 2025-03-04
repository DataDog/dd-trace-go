package llmobs

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// ExportedSpan represents a simplified representation of a span for export
type ExportedSpan struct {
	SpanID  string `json:"span_id"`
	TraceID string `json:"trace_id"`
}

// ExportSpan returns a simple representation of a span to export its span and trace IDs.
// If no span is provided, it will try to use the current active LLM-type span.
func ExportSpan(span ddtrace.Span) (*ExportedSpan, error) {
	if span == nil {
		return nil, fmt.Errorf("no span provided")
	}

	// Check if this is an LLM span
	if spanTypeGetter, ok := span.(interface{ SpanType() string }); ok {
		if spanTypeGetter.SpanType() != SpanTypeLLM {
			log.Debug("Span is not an LLM span")
			return nil, fmt.Errorf("span must be an LLM span")
		}
	}

	// Export the span context
	ctx := span.Context()
	if ctx == nil {
		return nil, fmt.Errorf("span context is nil")
	}

	// Create the exported span
	return &ExportedSpan{
		SpanID:  fmt.Sprintf("%d", ctx.SpanID()),
		TraceID: fmt.Sprintf("%d", ctx.TraceID()),
	}, nil
}

// SubmitEvaluationForSpan submits an evaluation metric for a specific span
func SubmitEvaluationForSpan(span ddtrace.Span, label string, metricType string, value interface{}, opts SubmitEvaluationOptions) error {
	// Export the span first
	exportedSpan, err := ExportSpan(span)
	if err != nil {
		return fmt.Errorf("failed to export span: %w", err)
	}

	// Update the options with the span's ID and trace ID
	opts.SpanID = exportedSpan.SpanID
	opts.TraceID = exportedSpan.TraceID
	opts.Label = label
	opts.MetricType = metricType
	opts.Value = value

	// Submit the evaluation
	return SubmitEvaluation(opts)
}

// RecordObject records an object (input or output) in the link tracker
func RecordObject(span ddtrace.Span, obj interface{}, inputOrOutput string) {
	llmObs := GetLLMObs()
	llmObs.RLock()
	defer llmObs.RUnlock()

	if !llmObs.enabled {
		log.Debug("RecordObject called when LLMObs is not enabled")
		return
	}

	if obj == nil {
		return
	}

	// Get exported span
	exportedSpan, err := ExportSpan(span)
	if err != nil {
		log.Debug("Failed to export span: %v", err)
		return
	}

	// Get span links from the object
	spanLinks := llmObs.linkTracker.getSpanLinksFromObject(obj)

	// Filter and update span links
	var filteredLinks []SpanLink
	for _, link := range spanLinks {
		// Check if this link is from input and we're recording output
		if attr, ok := link.Attributes["from"]; ok && attr == "input" && inputOrOutput == "output" {
			continue
		}

		// Add the link to filtered links
		filteredLinks = append(filteredLinks, link)
	}

	// Tag the span with the links
	llmObs.linkTracker.tagSpanLinks(span, filteredLinks)

	// Add this span as a link to the object
	llmObs.linkTracker.addSpanLinksToObject(obj, []SpanLink{
		{
			TraceID: exportedSpan.TraceID,
			SpanID:  exportedSpan.SpanID,
			Attributes: map[string]interface{}{
				"from": inputOrOutput,
			},
		},
	})
}
