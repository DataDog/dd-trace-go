// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
)

// ExampleSpanContext_httpRequestInterop demonstrates that SpanContext works
// seamlessly as context.Context in the HTTP stack without any modifications.
func ExampleSpanContext_httpRequestInterop() {
	tracer, _ := newTracer()
	defer tracer.Stop()

	// Create SpanContext from tracing
	span := tracer.StartSpan("http.handler")
	defer span.Finish()

	spanCtx := span.Context() // This IS context.Context

	// 1. SpanContext works with http.Request.WithContext() - no breaks
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req = req.WithContext(spanCtx) // SpanContext used as context.Context

	// 2. SpanContext works with context timeout/cancellation - no breaks
	ctxWithTimeout, cancel := context.WithTimeout(spanCtx, 5*time.Second)
	defer cancel()
	req = req.WithContext(ctxWithTimeout) // Derived context works too

	// 3. SpanContext preserves HTTP request context values - no breaks
	originalRequestID := "req-12345"
	enrichedCtx := context.WithValue(spanCtx, "request-id", originalRequestID)
	req = req.WithContext(enrichedCtx)

	// Verify values are preserved through HTTP stack
	extractedID := req.Context().Value("request-id").(string) // Works!
	_, _ = extractedID, originalRequestID                     // Use values
}

// ExampleSpanContext_baggageAPIsInterop demonstrates that ALL baggage APIs
// work seamlessly with SpanContext as context.Context.
func ExampleSpanContext_baggageAPIsInterop() {
	tracer, _ := newTracer()
	defer tracer.Stop()

	// Start with SpanContext
	span := tracer.StartSpan("operation")
	defer span.Finish()

	ctx := span.Context() // SpanContext that implements context.Context

	// ALL baggage package functions work with SpanContext unchanged:

	// 1. Set baggage - SpanContext implements context.Context seamlessly
	updatedCtx := baggage.Set(ctx, "user-id", "12345")
	// updatedCtx is the same SpanContext (returned as context.Context interface)

	// 2. Get baggage - works directly
	userID, exists := baggage.Get(ctx, "user-id")
	_, _ = userID, exists // "12345", true

	// 3. All baggage - returns W3C baggage map
	allBaggage := baggage.All(ctx)
	_ = allBaggage // map["user-id":"12345"]

	// 4. Remove baggage - works seamlessly
	clearedCtx := baggage.Remove(updatedCtx, "user-id")

	// 5. Clear all baggage - works seamlessly
	emptyCtx := baggage.Clear(clearedCtx)
	_ = emptyCtx

	// 6. Baggage iteration (W3C only) - works for span tags
	baggage.ForeachBaggageItem(ctx, func(k, v string) bool {
		// Only W3C baggage items appear here
		return true
	})

	// The key insight: ctx is STILL the same SpanContext throughout!
	// No conversions, no wrapping, no breaks - pure interoperability
}

// ExampleSpanContext_httpMiddlewareIntegration shows SpanContext working
// in real HTTP middleware chains without any special handling.
func ExampleSpanContext_httpMiddlewareIntegration() {
	tracer, _ := newTracer()
	defer tracer.Stop()

	// Standard HTTP middleware that expects context.Context
	loggingMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add request ID to context (standard pattern)
			ctx := context.WithValue(r.Context(), "request-id", "12345")
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}

	// Tracing middleware using SpanContext
	tracingMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract traces + baggage
			spanCtx, err := tracer.Extract(HTTPHeadersCarrier(r.Header))
			if err == nil && spanCtx.IsValid() {
				// SpanContext as context.Context - seamless integration
				r = r.WithContext(spanCtx)
			}
			next.ServeHTTP(w, r)
		})
	}

	// Business logic handler
	businessHandler := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Could be regular context.Context OR SpanContext - works either way!

		// Access values (works with both)
		requestID := ctx.Value("request-id")

		// Use baggage APIs (works with both)
		ctx = baggage.Set(ctx, "processed", "true")
		userID, _ := baggage.Get(ctx, "user-id")

		// Start spans (works with both)
		span := tracer.StartSpan("business.logic", ChildOf(SpanContextFromContext(ctx)))
		defer span.Finish()

		_, _ = requestID, userID // Use values
	}

	// Chain middlewares - SpanContext integrates seamlessly
	handler := loggingMiddleware(tracingMiddleware(http.HandlerFunc(businessHandler)))

	// Execute with cancelled context to prevent blocking
	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	req.Header.Set("baggage", "user-id=67890")

	// Use cancelled context for immediate termination
	cancelledCtx, cancel := context.WithCancel(req.Context())
	cancel() // Cancel immediately
	req = req.WithContext(cancelledCtx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// httptest is designed for this - safe execution without blocking
}

// ExampleSpanContext_distributedTracingFlow shows complete service-to-service
// propagation using only existing APIs with SpanContext as context.Context.
func ExampleSpanContext_distributedTracingFlow() {
	tracer, _ := newTracer()
	defer tracer.Stop()

	// === SERVICE A (Incoming Request) ===

	// Incoming request with distributed trace + baggage
	incomingReq := httptest.NewRequest("GET", "http://service-a/api", nil)
	incomingReq.Header.Set("baggage", "correlation-id=trace-001,tenant=customer-x")
	incomingReq.Header.Set("x-datadog-trace-id", "123456789")
	incomingReq.Header.Set("x-datadog-parent-id", "987654321")

	// Extract using existing API
	incomingSpanCtx, _ := tracer.Extract(HTTPHeadersCarrier(incomingReq.Header))

	// Use SpanContext directly as request context (seamless!)
	incomingReq = incomingReq.WithContext(incomingSpanCtx)

	// === SERVICE A to SERVICE B (Outgoing Request) ===
	makeServiceBCall := func(ctx context.Context) {
		// Create outgoing request
		outgoingReq, _ := http.NewRequest("POST", "http://service-b/process", nil)

		// Inject trace + baggage using existing API - accepts context.Context
		// Works seamlessly whether ctx is SpanContext or regular context!
		err := InjectContext(ctx, HTTPHeadersCarrier(outgoingReq.Header))
		if err == nil {
			// Headers now contain:
			// - Trace context (x-datadog-trace-id, etc.)
			// - All baggage items in baggage header
			// - Maintains distributed tracing chain
		}

		// Execute request (in real code)
		_ = outgoingReq
	}

	// === SERVICE A (Processing) ===

	serviceAHandler := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context() // Could be SpanContext or regular context

		// Start span (works with both)
		span := tracer.StartSpan("service-a.process", ChildOf(SpanContextFromContext(ctx)))
		defer span.Finish()

		// Access incoming baggage (works seamlessly)
		correlationID, _ := baggage.Get(ctx, "correlation-id")
		tenant, _ := baggage.Get(ctx, "tenant")

		// Add service-specific baggage
		ctx = baggage.Set(ctx, "processed-by", "service-a")

		// Make call to Service B
		makeServiceBCall(ctx)

		_, _ = correlationID, tenant // Use values
	}

	// Execute with cancelled context to prevent blocking
	cancelledCtx, cancel := context.WithCancel(incomingReq.Context())
	cancel() // Cancel immediately for safe example execution
	incomingReq = incomingReq.WithContext(cancelledCtx)

	w := httptest.NewRecorder()
	serviceAHandler(w, incomingReq)
	// httptest with cancelled context - safe and fast

	// Key insight: SpanContext integrates seamlessly throughout the entire
	// HTTP stack without requiring any special handling or conversions!
}

// ExampleSpanContext_standardContextOperations shows that SpanContext
// supports all standard context.Context operations.
func ExampleSpanContext_standardContextOperations() {
	tracer, _ := newTracer()
	defer tracer.Stop()

	span := tracer.StartSpan("operation")
	defer span.Finish()

	spanCtx := span.Context() // SpanContext implementing context.Context

	// 1. Deadline/timeout operations work
	ctxWithTimeout, cancel := context.WithTimeout(spanCtx, 10*time.Second)
	defer cancel()

	deadline, hasDeadline := ctxWithTimeout.Deadline()
	_ = deadline    // Works!
	_ = hasDeadline // true

	// 2. Cancellation works
	ctxWithCancel, cancel2 := context.WithCancel(spanCtx)
	defer cancel2()

	done := ctxWithCancel.Done()
	_ = done // Works! Returns cancellation channel

	// 3. Value storage/retrieval works
	enrichedCtx := context.WithValue(spanCtx, "key", "value")
	retrievedValue := enrichedCtx.Value("key")
	_ = retrievedValue // "value"

	// 4. Baggage operations work on derived contexts
	enrichedCtx = baggage.Set(enrichedCtx, "baggage-key", "baggage-value")
	baggageValue, _ := baggage.Get(enrichedCtx, "baggage-key")
	_ = baggageValue // "baggage-value"

	// Key insight: SpanContext is a first-class context.Context citizen!
}
