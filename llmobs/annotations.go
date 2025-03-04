package llmobs

import (
	"encoding/json"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// AnnotationOptions contains options for annotating spans
type AnnotationOptions struct {
	Tags       map[string]interface{}
	Prompt     map[string]interface{}
	InputData  interface{}
	OutputData interface{}
	Metadata   map[string]interface{}
	Metrics    map[string]float64
	Name       string
}

// AnnotationContext represents an active annotation context
type AnnotationContext struct {
	register   func()
	deregister func()
}

// Enter begins a new annotation context
func (a *AnnotationContext) Enter() {
	a.register()
}

// Exit ends the annotation context
func (a *AnnotationContext) Exit() {
	a.deregister()
}

// Annotate adds metadata, inputs, outputs, tags, and metrics to a span
func Annotate(span ddtrace.Span, opts AnnotationOptions) {
	if span == nil {
		log.Debug("Cannot annotate nil span")
		return
	}

	// Span type check - should be an LLM span
	spanType, ok := span.(interface{ SpanType() string })
	if !ok || spanType.SpanType() != SpanTypeLLM {
		log.Debug("Span is not an LLM span")
		return
	}

	// Check if span is finished
	if isFinished, ok := span.(interface{ Finished() bool }); ok && isFinished.Finished() {
		log.Debug("Cannot annotate finished span")
		return
	}

	// Set name if provided
	if opts.Name != "" {
		span.SetOperationName(opts.Name)
	}

	// Set metadata
	if opts.Metadata != nil {
		metadataJSON, err := json.Marshal(opts.Metadata)
		if err == nil {
			span.SetTag(keyMetadata, string(metadataJSON))
		} else {
			log.Debug("Failed to marshal metadata: %v", err)
		}
	}

	// Set metrics
	if opts.Metrics != nil {
		for k, v := range opts.Metrics {
			span.SetTag(fmt.Sprintf("metrics.%s", k), v)
		}
	}

	// Set tags
	if opts.Tags != nil {
		for k, v := range opts.Tags {
			span.SetTag(k, v)
		}
	}

	// Set prompt
	if opts.Prompt != nil {
		promptJSON, err := json.Marshal(opts.Prompt)
		if err == nil {
			span.SetTag(keyInputPrompt, string(promptJSON))
		} else {
			log.Debug("Failed to marshal prompt: %v", err)
		}
	}

	// Set input/output data based on span kind
	if spanKind, ok := getTagValue(span, keySpanKind); ok {
		switch spanKind {
		case SpanKindLLM:
			setLLMData(span, opts.InputData, opts.OutputData)
		case SpanKindEmbedding:
			setEmbeddingData(span, opts.InputData, opts.OutputData)
		case SpanKindRetrieval:
			setRetrievalData(span, opts.InputData, opts.OutputData)
		default:
			setGenericData(span, opts.InputData, opts.OutputData)
		}
	}
}

// AnnotationContext creates a new annotation context that will apply the specified
// attributes to all LLM spans created within the context
func (l *LLMObs) AnnotationContext(opts AnnotationOptions) *AnnotationContext {
	// Generate a unique ID for this annotation
	annotationID := rand64bits()
	contextID := fmt.Sprintf("%d", annotationID)

	// Create register and deregister functions
	register := func() {
		l.Lock()
		defer l.Unlock()

		l.annotations = append(l.annotations, annotation{
			id:        annotationID,
			contextID: contextID,
			tags:      opts.Tags,
			prompt:    opts.Prompt,
			name:      opts.Name,
		})
	}

	deregister := func() {
		l.Lock()
		defer l.Unlock()

		for i, a := range l.annotations {
			if a.id == annotationID {
				// Remove the annotation
				l.annotations = append(l.annotations[:i], l.annotations[i+1:]...)
				return
			}
		}
		log.Debug("Failed to find annotation to deregister")
	}

	return &AnnotationContext{
		register:   register,
		deregister: deregister,
	}
}

// applyAnnotations applies pending annotations to a span
func (l *LLMObs) applyAnnotations(span ddtrace.Span) {
	for _, a := range l.annotations {
		Annotate(span, AnnotationOptions{
			Tags:   a.tags,
			Prompt: a.prompt,
			Name:   a.name,
		})
	}
}

// getTagValue attempts to retrieve a tag value from a span
func getTagValue(span ddtrace.Span, key string) (string, bool) {
	// This is an internal helper function that would need to access span.Meta
	// but since we don't have direct access to that in the interface,
	// we would need to use reflection or other methods to retrieve tag values.

	// For now, we'll just return a default value for demonstration
	return "", false
}

// setLLMData sets LLM-specific input and output data on a span
func setLLMData(span ddtrace.Span, inputData, outputData interface{}) {
	if inputData != nil {
		messages, err := formatMessages(inputData)
		if err == nil {
			messagesJSON, _ := json.Marshal(messages)
			span.SetTag(keyInputMessages, string(messagesJSON))
		} else {
			log.Debug("Failed to format input messages: %v", err)
		}
	}

	if outputData != nil {
		messages, err := formatMessages(outputData)
		if err == nil {
			messagesJSON, _ := json.Marshal(messages)
			span.SetTag(keyOutputMessages, string(messagesJSON))
		} else {
			log.Debug("Failed to format output messages: %v", err)
		}
	}
}

// setEmbeddingData sets embedding-specific input and output data on a span
func setEmbeddingData(span ddtrace.Span, inputData, outputData interface{}) {
	if inputData != nil {
		documents, err := formatDocuments(inputData)
		if err == nil {
			documentsJSON, _ := json.Marshal(documents)
			span.SetTag(keyInputDocuments, string(documentsJSON))
		} else {
			log.Debug("Failed to format input documents: %v", err)
		}
	}

	if outputData != nil {
		// For embeddings, output is typically a value
		outputJSON, err := json.Marshal(outputData)
		if err == nil {
			span.SetTag(keyOutputValue, string(outputJSON))
		} else {
			log.Debug("Failed to marshal output data: %v", err)
		}
	}
}

// setRetrievalData sets retrieval-specific input and output data on a span
func setRetrievalData(span ddtrace.Span, inputData, outputData interface{}) {
	if inputData != nil {
		inputJSON, err := json.Marshal(inputData)
		if err == nil {
			span.SetTag(keyInputValue, string(inputJSON))
		} else {
			log.Debug("Failed to marshal input data: %v", err)
		}
	}

	if outputData != nil {
		documents, err := formatDocuments(outputData)
		if err == nil {
			documentsJSON, _ := json.Marshal(documents)
			span.SetTag(keyOutputDocuments, string(documentsJSON))
		} else {
			log.Debug("Failed to format output documents: %v", err)
		}
	}
}

// setGenericData sets generic input and output data on a span
func setGenericData(span ddtrace.Span, inputData, outputData interface{}) {
	if inputData != nil {
		inputJSON, err := json.Marshal(inputData)
		if err == nil {
			span.SetTag(keyInputValue, string(inputJSON))
		} else {
			log.Debug("Failed to marshal input data: %v", err)
		}
	}

	if outputData != nil {
		outputJSON, err := json.Marshal(outputData)
		if err == nil {
			span.SetTag(keyOutputValue, string(outputJSON))
		} else {
			log.Debug("Failed to marshal output data: %v", err)
		}
	}
}

// formatMessages formats LLM messages from various input types
func formatMessages(data interface{}) ([]map[string]string, error) {
	var messages []map[string]string

	switch v := data.(type) {
	case string:
		// Single string message
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": v,
		})
	case map[string]string:
		// Single message as map
		if content, ok := v["content"]; ok {
			role := v["role"]
			if role == "" {
				role = "user"
			}
			messages = append(messages, map[string]string{
				"role":    role,
				"content": content,
			})
		}
	case []map[string]string:
		// List of messages
		messages = v
	case []interface{}:
		// List of messages as interfaces
		for _, item := range v {
			if msg, ok := item.(map[string]interface{}); ok {
				message := make(map[string]string)
				for k, val := range msg {
					if strVal, ok := val.(string); ok {
						message[k] = strVal
					}
				}
				if message["content"] != "" {
					if message["role"] == "" {
						message["role"] = "user"
					}
					messages = append(messages, message)
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported message format")
	}

	return messages, nil
}

// formatDocuments formats document data from various input types
func formatDocuments(data interface{}) ([]map[string]interface{}, error) {
	var documents []map[string]interface{}

	switch v := data.(type) {
	case string:
		// Single string document
		documents = append(documents, map[string]interface{}{
			"text": v,
		})
	case []string:
		// List of string documents
		for _, doc := range v {
			documents = append(documents, map[string]interface{}{
				"text": doc,
			})
		}
	case map[string]interface{}:
		// Single document as map
		if text, ok := v["text"].(string); ok && text != "" {
			documents = append(documents, v)
		}
	case []map[string]interface{}:
		// List of documents
		documents = v
	case []interface{}:
		// List of documents as interfaces
		for _, item := range v {
			if doc, ok := item.(map[string]interface{}); ok {
				if text, ok := doc["text"].(string); ok && text != "" {
					documents = append(documents, doc)
				}
			} else if strDoc, ok := item.(string); ok {
				documents = append(documents, map[string]interface{}{
					"text": strDoc,
				})
			}
		}
	default:
		return nil, fmt.Errorf("unsupported document format")
	}

	return documents, nil
}
