// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package apimcallout provides an HTTP-based external security processor for
// Azure API Management (APIM) and Boomi API Gateway. Both gateways send HTTP
// callouts to this service, which runs the Datadog WAF (AppSec) and returns a
// decision: continue (with propagation headers) or block (with status code,
// headers, body).
package apimcallout

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageAzureAPIMCallout)
}

// Instrumentation returns the package's instrumentation instance.
func Instrumentation() *instrumentation.Instrumentation {
	return instr
}

// AppsecAPIMConfig contains configuration for the APIM callout processor.
type AppsecAPIMConfig struct {
	Context              context.Context
	BlockingUnavailable  bool
	BodyParsingSizeLimit *int
	RequestTimeout       time.Duration // TTL for request state cache entries (default: 30s)
}

// NewHandler creates an http.Handler that processes callout requests from
// Azure APIM or Boomi API Gateway. It exposes a single endpoint:
//
//	POST / - receives callout messages from the gateway
func NewHandler(config AppsecAPIMConfig) http.Handler {
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 30 * time.Second
	}

	h := &handler{
		config: config,
		messageProcessor: proxy.NewProcessor(proxy.ProcessorConfig{
			BlockingUnavailable:  config.BlockingUnavailable,
			BodyParsingSizeLimit: config.BodyParsingSizeLimit,
			Framework:            "azure/apim-callout",
			Context:              config.Context,
			ContinueMessageFunc:  func(_ context.Context, _ proxy.ContinueActionOptions) error { return nil },
			BlockMessageFunc:     func(_ context.Context, _ proxy.BlockActionOptions) error { return nil },
		}, instr),
		requestStateCache:    initRequestStateCache(config.Context, config.RequestTimeout),
		bodyParsingSizeLimit: config.BodyParsingSizeLimit,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /", h.handleCallout)
	return mux
}

type handler struct {
	config               AppsecAPIMConfig
	messageProcessor     proxy.Processor
	requestStateCache    *ttlcache.Cache[string, *requestStateEntry]
	bodyParsingSizeLimit *int
}

// requestStateEntry wraps a RequestState with an atomic flag so the response
// handler can claim exclusive ownership before the TTL eviction callback fires.
// This prevents the race where both the eviction goroutine and the HTTP handler
// goroutine call Close() on the same RequestState concurrently.
type requestStateEntry struct {
	rs      *proxy.RequestState
	claimed atomic.Bool
}

// initRequestStateCache creates a TTL cache for correlating request and response phases.
// The cache background goroutine is stopped when ctx is cancelled.
func initRequestStateCache(ctx context.Context, ttl time.Duration) *ttlcache.Cache[string, *requestStateEntry] {
	cache := ttlcache.New[string, *requestStateEntry](
		ttlcache.WithTTL[string, *requestStateEntry](ttl),
	)

	cache.OnEviction(func(_ context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *requestStateEntry]) {
		entry := item.Value()
		// Atomically claim ownership. If the handler already claimed this
		// entry via CompareAndSwap, our CAS fails and we skip Close().
		if !entry.claimed.CompareAndSwap(false, true) {
			return
		}
		_ = entry.rs.Close()
		if reason == ttlcache.EvictionReasonExpired {
			instr.Logger().Warn("apim_callout: request state expired for request_id %s, closing orphaned span\n", item.Key())
		}
	})

	go cache.Start()
	go func() {
		<-ctx.Done()
		cache.Stop()
		// Evict all remaining entries so their Close() callbacks fire,
		// releasing pinned WAF memory before GC runs finalizers.
		cache.DeleteAll()
	}()

	return cache
}

// writeJSON encodes and writes a JSON response.
func writeJSON(w http.ResponseWriter, statusCode int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(v)
}

// handleCallout processes POST / from the gateway and dispatches based on
// whether a request-id is present (two-tier dispatch).
func (h *handler) handleCallout(w http.ResponseWriter, r *http.Request) {
	var msg calloutMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		instr.Logger().Error("apim_callout: error decoding request: %s\n", err.Error())
		writeJSON(w, http.StatusBadRequest, calloutResult{})
		return
	}

	if msg.RequestID == "" {
		// Tier 1: new request
		h.handleNewRequest(w, r, &msg)
	} else {
		// Tier 2: continuation with existing request-id
		h.handleContinuation(w, &msg)
	}
}

// handleNewRequest processes a new callout message (no request-id yet).
// It runs the request headers phase and optionally the request body phase
// if the body is provided inline.
func (h *handler) handleNewRequest(w http.ResponseWriter, r *http.Request, msg *calloutMessage) {
	var addrs addressesRequestHeaders
	if err := json.Unmarshal(msg.Addresses, &addrs); err != nil {
		instr.Logger().Error("apim_callout: error decoding request addresses: %s\n", err.Error())
		writeJSON(w, http.StatusBadRequest, calloutResult{})
		return
	}

	reqHeaders := &messageRequestHeaders{
		addr:                 &addrs,
		gateway:              msg.Gateway,
		bodyParsingSizeLimit: h.bodyParsingSizeLimit,
	}

	reqState, err := h.messageProcessor.OnRequestHeaders(r.Context(), reqHeaders)
	if err != nil && err != io.EOF {
		instr.Logger().Error("apim_callout: error processing request headers: %s\n", err.Error())
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	// Ensure the RequestState is always closed unless ownership is transferred
	// to the cache for subsequent phases.
	cached := false
	defer func() {
		if !cached {
			_ = reqState.Close()
		}
	}()

	// If blocked (io.EOF), build block response
	if err == io.EOF {
		writeJSON(w, http.StatusOK, calloutResult{Block: buildBlockResult(reqState.BlockAction())})
		return
	}

	requestID := uuid.New().String()

	// If the processor wants the request body AND it was provided inline, process it now.
	if reqState.State == proxy.MessageTypeRequestBody && hasRawBody(addrs.Body) {
		bodyBytes, decErr := decodeRawBase64Body(addrs.Body)
		if decErr != nil {
			instr.Logger().Error("apim_callout: error decoding request body base64: %s\n", decErr.Error())
		} else if bodyBytes != nil {
			bodyMsg := &messageBody{body: bodyBytes, m: proxy.MessageTypeRequestBody}
			if bodyErr := h.messageProcessor.OnRequestBody(bodyMsg, &reqState); bodyErr != nil {
				if bodyErr == io.EOF {
					writeJSON(w, http.StatusOK, calloutResult{Block: buildBlockResult(reqState.BlockAction())})
					return
				}
				instr.Logger().Error("apim_callout: error processing request body: %s\n", bodyErr.Error())
			}
		}
	}

	// Build continue response with propagation headers
	propHeaders, err := reqState.PropagationHeaders()
	if err != nil {
		instr.Logger().Warn("apim_callout: error getting propagation headers: %s\n", err.Error())
	}

	result := calloutResult{
		RequestID:        requestID,
		PropagateHeaders: propHeaders,
	}

	// If the processor still wants the request body but it wasn't provided inline,
	// tell the gateway how much body to send.
	if reqState.State == proxy.MessageTypeRequestBody && !hasRawBody(addrs.Body) {
		result.AllowedBodySize = h.bodyParsingSizeLimit
	}

	// Transfer ownership to the cache for subsequent phases
	h.requestStateCache.Set(requestID, &requestStateEntry{rs: &reqState}, ttlcache.DefaultTTL)
	cached = true

	writeJSON(w, http.StatusOK, result)
}

// handleContinuation processes a callout message with an existing request-id.
// It dispatches to the appropriate phase handler based on the cached request state.
func (h *handler) handleContinuation(w http.ResponseWriter, msg *calloutMessage) {
	// Look up request state from cache. Atomically claim the entry before
	// deleting it so the eviction callback won't race with us on Close().
	item := h.requestStateCache.Get(msg.RequestID)
	if item == nil {
		instr.Logger().Debug("apim_callout: no request state found for request_id %q, returning continue\n", msg.RequestID)
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	entry := item.Value()
	if !entry.claimed.CompareAndSwap(false, true) {
		// Already claimed by another goroutine (e.g., eviction), fail-open.
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	reqState := entry.rs

	// Log a warning if the declared phase is truly unexpected.
	// Valid transitions that should NOT warn:
	//   - State is a request phase and declared is a response phase (gateway
	//     moved from request to response processing, normal 4-call pattern)
	//   - Declared phase matches the current state exactly
	// Dispatch is based on reqState.State, not the phase field, so this is
	// informational only and does not affect processing.
	if msg.Phase != "" && msg.Phase != reqState.State.String() {
		// A request→response transition is expected when the gateway skips
		// directly from inbound to outbound processing.
		if !(reqState.State.Request() && (msg.Phase == proxy.MessageTypeResponseHeaders.String() || msg.Phase == proxy.MessageTypeResponseBody.String())) {
			instr.Logger().Warn("apim_callout: phase mismatch for request_id %q: declared %q, expected %q\n", msg.RequestID, msg.Phase, reqState.State.String())
		}
	}

	switch {
	case reqState.State == proxy.MessageTypeRequestBody:
		h.handleRequestBodyPhase(w, msg, reqState)

	case reqState.State == proxy.MessageTypeResponseBody:
		h.handleResponseBodyPhase(w, msg, reqState)

	case reqState.State.Request() || reqState.State == proxy.MessageTypeResponseHeaders:
		// Request phase complete (state is RequestHeaders/RequestTrailers after
		// OnRequestHeaders or OnRequestBody with GetEndOfStream=true), or
		// ResponseHeaders if somehow re-entered. Proceed to response headers.
		h.handleResponseHeadersPhase(w, msg, reqState)

	default:
		// MessageTypeFinished, MessageTypeBlocked, or unexpected state: fail-open.
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
	}
}

// handleRequestBodyPhase handles continuation when the processor expects a request body.
func (h *handler) handleRequestBodyPhase(w http.ResponseWriter, msg *calloutMessage, reqState *proxy.RequestState) {
	// Check if the gateway skipped the body phase and jumped to response headers.
	// This happens when the addresses contain a status_code field.
	var respAddrs addressesResponseHeaders
	if err := json.Unmarshal(msg.Addresses, &respAddrs); err == nil && respAddrs.StatusCode > 0 {
		// Gateway skipped request body phase, go directly to response headers.
		h.processResponseHeaders(w, msg, reqState, &respAddrs)
		return
	}

	// Normal request body phase
	var addrs addressesBody
	if err := json.Unmarshal(msg.Addresses, &addrs); err != nil {
		instr.Logger().Error("apim_callout: error decoding body addresses: %s\n", err.Error())
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	bodyBytes, decErr := decodeRawBase64Body(addrs.Body)
	if decErr != nil {
		instr.Logger().Error("apim_callout: error decoding request body base64: %s\n", decErr.Error())
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	if bodyBytes != nil {
		bodyMsg := &messageBody{body: bodyBytes, m: proxy.MessageTypeRequestBody}
		if bodyErr := h.messageProcessor.OnRequestBody(bodyMsg, reqState); bodyErr != nil {
			if bodyErr == io.EOF {
				h.requestStateCache.Delete(msg.RequestID)
				block := buildBlockResult(reqState.BlockAction())
				_ = reqState.Close()
				writeJSON(w, http.StatusOK, calloutResult{Block: block})
				return
			}
			instr.Logger().Error("apim_callout: error processing request body: %s\n", bodyErr.Error())
		}
	}

	// More phases coming, keep in cache (unclaim so eviction can work again)
	entry := &requestStateEntry{rs: reqState}
	h.requestStateCache.Set(msg.RequestID, entry, ttlcache.DefaultTTL)
	writeJSON(w, http.StatusOK, calloutResult{})
}

// handleResponseHeadersPhase handles continuation when the processor expects response headers.
func (h *handler) handleResponseHeadersPhase(w http.ResponseWriter, msg *calloutMessage, reqState *proxy.RequestState) {
	var addrs addressesResponseHeaders
	if err := json.Unmarshal(msg.Addresses, &addrs); err != nil {
		instr.Logger().Error("apim_callout: error decoding response addresses: %s\n", err.Error())
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	h.processResponseHeaders(w, msg, reqState, &addrs)
}

// processResponseHeaders is the shared logic for processing response headers,
// used by both the response headers phase and the request body phase (when the
// gateway skips body and jumps to response).
func (h *handler) processResponseHeaders(w http.ResponseWriter, msg *calloutMessage, reqState *proxy.RequestState, addrs *addressesResponseHeaders) {
	respHeaders := &messageResponseHeaders{
		addr:                 addrs,
		bodyParsingSizeLimit: h.bodyParsingSizeLimit,
	}

	err := h.messageProcessor.OnResponseHeaders(respHeaders, reqState)
	if err != nil && err != io.EOF {
		instr.Logger().Error("apim_callout: error processing response headers: %s\n", err.Error())
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	// If blocked
	if err == io.EOF && reqState.State == proxy.MessageTypeBlocked {
		h.requestStateCache.Delete(msg.RequestID)
		block := buildBlockResult(reqState.BlockAction())
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{Block: block})
		return
	}

	// If response body is needed and body was provided inline
	if reqState.State == proxy.MessageTypeResponseBody && hasRawBody(addrs.Body) {
		bodyBytes, decErr := decodeRawBase64Body(addrs.Body)
		if decErr != nil {
			instr.Logger().Error("apim_callout: error decoding response body base64: %s\n", decErr.Error())
		} else if bodyBytes != nil {
			bodyMsg := &messageBody{body: bodyBytes, m: proxy.MessageTypeResponseBody}
			if bodyErr := h.messageProcessor.OnResponseBody(bodyMsg, reqState); bodyErr != nil {
				if bodyErr == io.EOF && reqState.State == proxy.MessageTypeBlocked {
					h.requestStateCache.Delete(msg.RequestID)
					block := buildBlockResult(reqState.BlockAction())
					_ = reqState.Close()
					writeJSON(w, http.StatusOK, calloutResult{Block: block})
					return
				}
				if bodyErr != io.EOF {
					instr.Logger().Error("apim_callout: error processing response body: %s\n", bodyErr.Error())
				}
			}
		}
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	// If response body is needed but not provided inline
	if reqState.State == proxy.MessageTypeResponseBody {
		propHeaders, propErr := reqState.PropagationHeaders()
		if propErr != nil {
			instr.Logger().Warn("apim_callout: error getting propagation headers: %s\n", propErr.Error())
		}
		// Keep in cache for the body phase
		entry := &requestStateEntry{rs: reqState}
		h.requestStateCache.Set(msg.RequestID, entry, ttlcache.DefaultTTL)
		writeJSON(w, http.StatusOK, calloutResult{
			AllowedBodySize:  h.bodyParsingSizeLimit,
			PropagateHeaders: propHeaders,
		})
		return
	}

	// io.EOF but not blocked and not response body: response phase complete (no body needed)
	h.requestStateCache.Delete(msg.RequestID)
	propHeaders, propErr := reqState.PropagationHeaders()
	if propErr != nil {
		instr.Logger().Warn("apim_callout: error getting propagation headers: %s\n", propErr.Error())
	}
	_ = reqState.Close()
	writeJSON(w, http.StatusOK, calloutResult{PropagateHeaders: propHeaders})
}

// handleResponseBodyPhase handles continuation when the processor expects a response body.
func (h *handler) handleResponseBodyPhase(w http.ResponseWriter, msg *calloutMessage, reqState *proxy.RequestState) {
	var addrs addressesBody
	if err := json.Unmarshal(msg.Addresses, &addrs); err != nil {
		instr.Logger().Error("apim_callout: error decoding body addresses: %s\n", err.Error())
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	bodyBytes, decErr := decodeRawBase64Body(addrs.Body)
	if decErr != nil {
		instr.Logger().Error("apim_callout: error decoding response body base64: %s\n", decErr.Error())
		h.requestStateCache.Delete(msg.RequestID)
		_ = reqState.Close()
		writeJSON(w, http.StatusOK, calloutResult{})
		return
	}

	if bodyBytes != nil {
		bodyMsg := &messageBody{body: bodyBytes, m: proxy.MessageTypeResponseBody}
		if bodyErr := h.messageProcessor.OnResponseBody(bodyMsg, reqState); bodyErr != nil {
			if bodyErr == io.EOF && reqState.State == proxy.MessageTypeBlocked {
				h.requestStateCache.Delete(msg.RequestID)
				block := buildBlockResult(reqState.BlockAction())
				_ = reqState.Close()
				writeJSON(w, http.StatusOK, calloutResult{Block: block})
				return
			}
			if bodyErr != io.EOF {
				instr.Logger().Error("apim_callout: error processing response body: %s\n", bodyErr.Error())
			}
		}
	}

	h.requestStateCache.Delete(msg.RequestID)
	_ = reqState.Close()
	writeJSON(w, http.StatusOK, calloutResult{})
}

// buildBlockResult converts proxy.BlockActionOptions into a blockResult for the JSON response.
func buildBlockResult(opts proxy.BlockActionOptions) *blockResult {
	br := &blockResult{
		Status:  opts.StatusCode,
		Headers: opts.Headers,
	}
	if len(opts.Body) > 0 {
		br.Content = base64.StdEncoding.EncodeToString(opts.Body)
	}
	return br
}
