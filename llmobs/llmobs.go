// Package llmobs provides tracing capabilities for LLM-based applications.
// It extends Datadog's tracing capabilities to provide specific observability
// features for Large Language Models, embeddings, retrievals, agents, and other
// components of LLM-based applications.
package llmobs

import (
	"os"
	"sync"
)

// Constants for span types and keys
const (
	// SpanType for LLM operations
	SpanTypeLLM = "llm"

	// Span kinds
	SpanKindLLM       = "llm"
	SpanKindTool      = "tool"
	SpanKindTask      = "task"
	SpanKindAgent     = "agent"
	SpanKindWorkflow  = "workflow"
	SpanKindEmbedding = "embedding"
	SpanKindRetrieval = "retrieval"

	// Context keys for storing LLM-specific data
	keySpanKind        = "span.kind"
	keyModelName       = "model_name"
	keyModelProvider   = "model_provider"
	keySessionID       = "session_id"
	keyMLApp           = "ml_app"
	keyInputValue      = "input.value"
	keyOutputValue     = "output.value"
	keyInputMessages   = "input.messages"
	keyOutputMessages  = "output.messages"
	keyInputDocuments  = "input.documents"
	keyOutputDocuments = "output.documents"
	keyInputPrompt     = "input.prompt"
	keyMetadata        = "metadata"
	keyMetrics         = "metrics"
	keyTags            = "tags"
	keySpanLinks       = "span_links"
	keyParentID        = "parent_id"
	rootParentID       = "0"
)

// LLMObs provides the main functionality for LLM observability
type LLMObs struct {
	sync.RWMutex
	enabled             bool
	mlApp               string
	integrationsEnabled bool
	agentlessEnabled    bool
	site                string
	apiKey              string
	env                 string
	service             string
	spanWriter          *LLMObsSpanWriter
	evalMetricWriter    *LLMObsEvalMetricWriter
	evaluatorRunner     *EvaluatorRunner
	linkTracker         *LinkTracker
	annotations         []annotation
}

type annotation struct {
	id        uint64
	contextID string
	tags      map[string]interface{}
	prompt    map[string]interface{}
	name      string
}

// Global instance of LLMObs
var instance *LLMObs
var once sync.Once

// Options for configuring LLMObs
type Options struct {
	MLApp               string
	IntegrationsEnabled bool
	AgentlessEnabled    bool
	Site                string
	APIKey              string
	Env                 string
	Service             string
}

// GetLLMObs returns the global LLMObs instance
func GetLLMObs() *LLMObs {
	once.Do(func() {
		instance = &LLMObs{
			enabled:             false,
			mlApp:               os.Getenv("DD_LLMOBS_ML_APP"),
			integrationsEnabled: true,
			agentlessEnabled:    false,
			site:                os.Getenv("DD_SITE"),
			apiKey:              os.Getenv("DD_API_KEY"),
			env:                 os.Getenv("DD_ENV"),
			service:             os.Getenv("DD_SERVICE"),
			spanWriter:          nil,
			evalMetricWriter:    nil,
			evaluatorRunner:     nil,
			linkTracker:         newLinkTracker(),
			annotations:         []annotation{},
		}
	})
	return instance
}
