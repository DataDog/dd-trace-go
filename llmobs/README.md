# Datadog LLM Observability Go SDK

## Architecture Overview

We've designed a Go implementation of Datadog's LLM Observability SDK that closely follows the patterns established in the Python version while adhering to Go idioms and Datadog's Go tracer architecture. Here's an overview of the key components:

### Core Components

1. **Main LLMObs Singleton**
   - Central controller that manages the SDK's state and services
   - Configurable through options and environment variables
   - Follows the singleton pattern with thread-safe access

2. **Span Creation Functions**
   - `LLM()` - For LLM model interactions
   - `Tool()` - For external API or tool calls
   - `Task()` - For standalone operations
   - `Agent()` - For agent workflows
   - `Workflow()` - For predefined sequences
   - `Embedding()` - For embedding model interactions
   - `Retrieval()` - For vector database queries

3. **Annotation System**
   - `Annotate()` - Enriches spans with metadata, inputs, outputs, tags, and metrics
   - `AnnotationContext()` - Creates contexts for automatic annotation of spans

4. **Writers**
   - `LLMObsSpanWriter` - Buffers and sends span events
   - `LLMObsEvalMetricWriter` - Buffers and sends evaluation metrics

5. **Evaluation System**
   - `EvaluatorRunner` - Processes spans for evaluation
   - `SubmitEvaluation()` - Allows custom evaluation metrics to be submitted

6. **Object Linking**
   - `LinkTracker` - Tracks relationships between objects and spans
   - `RecordObject()` - Records objects for tracking

## Key Features

1. **Span Type Specialization**
   - Specialized span types for different LLM-related operations
   - Each span type has appropriate metadata specific to its purpose

2. **Rich Metadata**
   - Support for structured input/output data
   - Customizable tags and metrics
   - Prompt templating and variables

3. **Context Propagation**
   - Spans inherit context from parent spans
   - Session tracking through session IDs

4. **Evaluation**# Datadog LLM Observability Go SDK

## Architecture Overview

We've designed a Go implementation of Datadog's LLM Observability SDK that closely follows the patterns established in the Python version while adhering to Go idioms and Datadog's Go tracer architecture. Here's an overview of the key components:

### Core Components

1. **Main LLMObs Singleton**
   - Central controller that manages the SDK's state and services
   - Configurable through options and environment variables
   - Follows the singleton pattern with thread-safe access

2. **Span Creation Functions**
   - `LLM()` - For LLM model interactions
   - `Tool()` - For external API or tool calls
   - `Task()` - For standalone operations
   - `Agent()` - For agent workflows
   - `Workflow()` - For predefined sequences
   - `Embedding()` - For embedding model interactions
   - `Retrieval()` - For vector database queries

3. **Annotation System**
   - `Annotate()` - Enriches spans with metadata, inputs, outputs, tags, and metrics
   - `AnnotationContext()` - Creates contexts for automatic annotation of spans

4. **Writers**
   - `LLMObsSpanWriter` - Buffers and sends span events
   - `LLMObsEvalMetricWriter` - Buffers and sends evaluation metrics

5. **Evaluation System**
   - `EvaluatorRunner` - Processes spans for evaluation
   - `SubmitEvaluation()` - Allows custom evaluation metrics to be submitted

6. **Object Linking**
   - `LinkTracker` - Tracks relationships between objects and spans
   - `RecordObject()` - Records objects for tracking

## Key Features

1. **Span Type Specialization**
   - Specialized span types for different LLM-related operations
   - Each span type has appropriate metadata specific to its purpose

2. **Rich Metadata**
   - Support for structured input/output data
   - Customizable tags and metrics
   - Prompt templating and variables

3. **Context Propagation**
   - Spans inherit context from parent spans
   - Session tracking through session IDs

4. **Evaluation**
   - Both automatic and manual evaluation support
   - Multiple metric types: categorical and score metrics

5. **Integration with Datadog Core**
   - Built on top of Datadog's Go tracing infrastructure
   - Compatible with existing Datadog APM features

## Usage Patterns

1. **Basic Tracing**
   ```go
   ctx, span := llmobs.LLM(ctx, llmobs.SpanOptions{
       Name:          "my-llm-call",
       ModelName:     "gpt-4",
       ModelProvider: "openai",
   })
   defer span.Finish()
   ```

2. **Enriching Spans**
   ```go
   llmobs.Annotate(span, llmobs.AnnotationOptions{
       InputData: userMessage,
       OutputData: aiResponse,
       Metrics: map[string]float64{
           "tokens": 150,
       },
   })
   ```

3. **Creating Annotation Contexts**
   ```go
   ctx := llmobs.GetLLMObs().AnnotationContext(llmobs.AnnotationOptions{
       Tags: map[string]interface{}{
           "user_id": "123",
       },
   })
   ctx.Enter()
   defer ctx.Exit()
   ```

4. **Evaluating Spans**
   ```go
   llmobs.SubmitEvaluation(llmobs.SubmitEvaluationOptions{
       Label:      "accuracy",
       MetricType: "score",
       Value:      0.95,
       SpanID:     exportedSpan.SpanID,
       TraceID:    exportedSpan.TraceID,
   })
   ```

This design provides a solid foundation for LLM Observability in Go applications, with a familiar interface for developers already using Datadog's tracing libraries.
   - Both automatic and manual evaluation support
   - Multiple metric types: categorical and score metrics

5. **Integration with Datadog Core**
   - Built on top of Datadog's Go tracing infrastructure
   - Compatible with existing Datadog APM features

## Usage Patterns

1. **Basic Tracing**
   ```go
   ctx, span := llmobs.LLM(ctx, llmobs.SpanOptions{
       Name:          "my-llm-call",
       ModelName:     "gpt-4",
       ModelProvider: "openai",
   })
   defer span.Finish()
   ```

2. **Enriching Spans**
   ```go
   llmobs.Annotate(span, llmobs.AnnotationOptions{
       InputData: userMessage,
       OutputData: aiResponse,
       Metrics: map[string]float64{
           "tokens": 150,
       },
   })
   ```

3. **Creating Annotation Contexts**
   ```go
   ctx := llmobs.GetLLMObs().AnnotationContext(llmobs.AnnotationOptions{
       Tags: map[string]interface{}{
           "user_id": "123",
       },
   })
   ctx.Enter()
   defer ctx.Exit()
   ```

4. **Evaluating Spans**
   ```go
   llmobs.SubmitEvaluation(llmobs.SubmitEvaluationOptions{
       Label:      "accuracy",
       MetricType: "score",
       Value:      0.95,
       SpanID:     exportedSpan.SpanID,
       TraceID:    exportedSpan.TraceID,
   })
   ```

This design provides a solid foundation for LLM Observability in Go applications, with a familiar interface for developers already using Datadog's tracing libraries.