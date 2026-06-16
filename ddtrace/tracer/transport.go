// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal/tracerstats"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/version"

	"github.com/tinylib/msgp/msgp"
)

const (
	// headerComputedTopLevel specifies that the client has marked top-level spans, when set.
	// Any non-empty value will mean 'yes'.
	headerComputedTopLevel = "Datadog-Client-Computed-Top-Level"

	// idempotencyKeyHeader marks a POST request as safe to replay. The Go HTTP
	// transport treats requests carrying this header as idempotent and will
	// transparently retry them on transient connection errors (e.g. an idle
	// keep-alive connection silently closed by the agent), provided the request
	// body can be re-read via req.GetBody. See
	// https://github.com/golang/go/issues/19943 and net/http.Request.isReplayable.
	// The agent ignores this header.
	idempotencyKeyHeader = "Idempotency-Key"
)

const (
	defaultHostname          = "localhost"
	defaultPort              = "8126"
	defaultAddress           = defaultHostname + ":" + defaultPort
	defaultURL               = "http://" + defaultAddress
	defaultHTTPTimeout       = 10 * time.Second              // defines the current timeout before giving up with the send process
	traceCountHeader         = "X-Datadog-Trace-Count"       // header containing the number of traces in the payload
	obfuscationVersionHeader = "Datadog-Obfuscation-Version" // header containing the version of obfuscation used, if any
	containerIDHeader        = "Datadog-Container-ID"
	entityIDHeader           = "Datadog-Entity-ID"
	containerTagsHashHeader  = "Datadog-Container-Tags-Hash"

	tracesAPIPath   = "/v0.4/traces"
	tracesAPIPathV1 = "/v1.0/traces"
	statsAPIPath    = "/v0.6/stats"
)

// ddTransport is an interface for communicating data to the Datadog agent
// using Datadog-specific protocols (msgpack traces, stats payloads).
type ddTransport interface {
	// send sends the msgpack-encoded payload p to the agent using the transport set up.
	// It returns a non-nil response body when no error occurred.
	send(p payload) (body io.ReadCloser, err error)
	// sendStats sends the given stats payload to the agent.
	// tracerObfuscationVersion is the version of obfuscation applied (0 if none was applied)
	sendStats(s *pb.ClientStatsPayload, tracerObfuscationVersion int) error
	// endpoint returns the URL to which the transport will send traces.
	endpoint() string
}

type httpTransport struct {
	traceURL string            // the delivery URL for traces
	statsURL string            // the delivery URL for stats
	client   *http.Client      // the HTTP client used in the POST
	headers  map[string]string // the Transport headers
}

// newHTTPTransport returns a new Transport implementation that sends traces
// to the given traceURL and stats to the given statsURL, using the provided
// *http.Client and headers. The caller is responsible for providing the
// appropriate headers (e.g. datadogHeaders() for Datadog mode, or OTLP
// headers resolved from config).
func newHTTPTransport(traceURL string, statsURL string, client *http.Client, headers map[string]string) *httpTransport {
	return &httpTransport{
		traceURL: traceURL,
		statsURL: statsURL,
		client:   client,
		headers:  headers,
	}
}

func datadogHeaders() map[string]string {
	h := map[string]string{
		"Datadog-Meta-Lang":             "go",
		"Datadog-Meta-Lang-Version":     strings.TrimPrefix(runtime.Version(), "go"),
		"Datadog-Meta-Lang-Interpreter": runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS,
		"Datadog-Meta-Tracer-Version":   version.Tag,
		"Content-Type":                  "application/msgpack",
	}
	if cid := internal.ContainerID(); cid != "" {
		h[containerIDHeader] = cid
	}
	if eid := internal.EntityID(); eid != "" {
		h[entityIDHeader] = eid
	}
	if extEnv := internal.ExternalEnvironment(); extEnv != "" {
		h["Datadog-External-Env"] = extEnv
	}
	return h
}

func setContainerHeaders(h http.Header) {
	if cid := internal.ContainerID(); cid != "" {
		h.Set(containerIDHeader, cid)
	}
	if eid := internal.EntityID(); eid != "" {
		h.Set(entityIDHeader, eid)
	}
}

func updateContainerTagsHash(h http.Header) {
	if hash := h.Get(containerTagsHashHeader); hash != "" {
		processtags.SetContainerTagsHash(hash)
	}
}

func (t *httpTransport) sendStats(p *pb.ClientStatsPayload, tracerObfuscationVersion int) error {
	var buf bytes.Buffer
	if err := msgp.Encode(&buf, p); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", t.statsURL, &buf)
	if err != nil {
		return err
	}
	for header, value := range t.headers {
		req.Header.Set(header, value)
	}
	if tracerObfuscationVersion > 0 {
		req.Header.Set(obfuscationVersionHeader, strconv.Itoa(tracerObfuscationVersion))
	}
	// Mark the POST replayable so net/http transparently retries on a fresh
	// connection when an idle UDS conn was silently closed by the agent.
	// http.NewRequest already populates req.GetBody for *bytes.Buffer bodies.
	//
	// Duplication risk: if the agent received and processed the payload before
	// resetting the connection (read-side ECONNRESET), a retry will deliver the
	// same ClientStatsPayload twice. The agent does not deduplicate stats the
	// way it deduplicates traces (by span ID), so a duplicate payload can
	// overcount metrics for that flush window. This is an accepted trade-off:
	// the read-side reset is the rarer path (stdlib already recovers from
	// pre-write failures transparently via the Idempotency-Key path), and a
	// transient one-window overcount that self-corrects on the next flush is
	// less harmful than silently dropping stats. For EPIPE and write-side
	// resets the retry is always safe — the bytes never reached the agent.
	req.Header.Set(idempotencyKeyHeader, newIdempotencyKey())
	resp, err := t.doWithStaleConnRetry(req)
	if err != nil {
		reportAPIErrorsMetric(resp, err, statsAPIPath)
		return err
	}
	defer resp.Body.Close()
	if code := resp.StatusCode; code >= 400 {
		reportAPIErrorsMetric(resp, err, statsAPIPath)
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := resp.Body.Read(msg)
		resp.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return fmt.Errorf("%s", txt)
	}
	return nil
}

func (t *httpTransport) send(p payload) (body io.ReadCloser, err error) {
	req, err := http.NewRequest("POST", t.traceURL, p)
	if err != nil {
		return nil, fmt.Errorf("cannot create http request: %s", err)
	}
	stats := p.stats()
	req.ContentLength = int64(stats.size)
	// Mark the POST replayable so net/http transparently retries on a fresh
	// connection when an idle UDS conn was silently closed by the agent.
	// http.NewRequest does not auto-populate GetBody for the custom payload
	// type, so we set it explicitly. The payload is returned directly (not
	// wrapped in io.NopCloser) so that the stdlib's request path calls
	// req.Body.Close() on it: payloadV04.Close is a no-op, and
	// payloadV1.Close signals the pool-return handoff via atomic.Or
	// (idempotent), which is required for payloadV1 to return to its pool.
	req.GetBody = func() (io.ReadCloser, error) {
		p.reset()
		return p, nil
	}
	for header, value := range t.headers {
		req.Header.Set(header, value)
	}
	req.Header.Set(traceCountHeader, strconv.Itoa(stats.itemCount))
	req.Header.Set(headerComputedTopLevel, "t")
	req.Header.Set(idempotencyKeyHeader, newIdempotencyKey())
	if t := getGlobalTracer(); t != nil {
		tc := t.TracerConf()
		if tc.TracingAsTransport || tc.CanComputeStats {
			// tracingAsTransport uses this header to disable the trace agent's stats computation
			// while making canComputeStats() always false to also disable client stats computation.
			req.Header.Set("Datadog-Client-Computed-Stats", "t")
		}
		droppedTraces := int(tracerstats.Count(tracerstats.AgentDroppedP0Traces))
		partialTraces := int(tracerstats.Count(tracerstats.PartialTraces))
		droppedSpans := int(tracerstats.Count(tracerstats.AgentDroppedP0Spans))
		if tt, ok := t.(*tracer); ok {
			if stats := tt.statsd; stats != nil {
				stats.Count("datadog.tracer.dropped_p0_traces", int64(droppedTraces),
					[]string{fmt.Sprintf("partial:%s", strconv.FormatBool(partialTraces > 0))}, 1)
				stats.Count("datadog.tracer.dropped_p0_spans", int64(droppedSpans), nil, 1)
			}
		}
		req.Header.Set("Datadog-Client-Dropped-P0-Traces", strconv.Itoa(droppedTraces))
		req.Header.Set("Datadog-Client-Dropped-P0-Spans", strconv.Itoa(droppedSpans))
	}
	response, err := t.doWithStaleConnRetry(req)
	if err != nil {
		reportAPIErrorsMetric(response, err, tracesAPIPath)
		return nil, err
	}
	if code := response.StatusCode; code >= 400 {
		reportAPIErrorsMetric(response, err, tracesAPIPath)
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := response.Body.Read(msg)
		response.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return nil, fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return nil, fmt.Errorf("%s", txt)
	}
	return response.Body, nil
}

// newIdempotencyKey returns a 32-char zero-padded hex string encoding 128 bits
// of randomness, suitable for use as an Idempotency-Key header value. The Go
// HTTP transport only checks for the header's presence (see
// net/http.Request.isReplayable), so any non-empty value works; the random
// value preserves the standard semantics of the header for any intermediary
// that does inspect it. The agent ignores this header.
func newIdempotencyKey() string {
	return fmt.Sprintf("%016x%016x", randUint64(), randUint64())
}

// staleConnRetryAttempts is the number of times doWithStaleConnRetry will
// re-issue a request after a transient connection error. Three retries are
// enough to absorb burst stale-conn scenarios (e.g. the agent killing several
// idle conns at once, or a series of stale conns queued in the idle pool under
// heavy concurrent load). The total request budget is therefore 4 attempts.
const staleConnRetryAttempts = 3

// doWithStaleConnRetry executes req and, on a transient connection error,
// rewinds the body via req.GetBody and retries on a fresh connection.
//
// Idempotency-Key + GetBody let net/http auto-recover from most stale-idle UDS
// races (the agent silently closes idle keep-alive conns under backpressure),
// but stdlib will not retry once any byte of the request has been written —
// even with Idempotency-Key set — because it can no longer classify the error
// as nothingWrittenError. These extra application-level retries cover that
// residual mid-write EPIPE/ECONNRESET window. See golang/go#19943.
//
// Retrying is safe: the agent dedups traces by span ID and ignores the
// Idempotency-Key header, so a duplicate payload is harmless. We only retry
// on the narrow set of errors that signal a connection torn down by the peer.
func (t *httpTransport) doWithStaleConnRetry(req *http.Request) (*http.Response, error) {
	resp, err := t.client.Do(req)
	for range staleConnRetryAttempts {
		// req.GetBody is nil when the caller did not set it explicitly and
		// the stdlib did not auto-populate it (only *bytes.Buffer,
		// *bytes.Reader, and *strings.Reader get auto-populated). When it is
		// nil we cannot rewind the body and must return the original error.
		if err == nil || !isTransientConnError(err) || req.GetBody == nil {
			return resp, err
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		body, gbErr := req.GetBody()
		if gbErr != nil {
			// Fall back to the original error — re-rewinding the body failed,
			// so the caller will see the actual transport error rather than a
			// confusing rewind failure.
			return resp, err
		}
		req.Body = body
		resp, err = t.client.Do(req)
	}
	return resp, err
}

// isTransientConnError reports whether err describes a connection torn down by
// the peer mid-request — typically EPIPE on write or ECONNRESET on read after
// an idle keep-alive UDS conn was silently closed by the agent. net.ErrClosed
// is also included: stdlib calls Close() on a broken persistConn before the
// error reaches the caller, so a concurrent writer racing against that close
// may see "use of closed network connection" instead of the underlying EPIPE.
//
// We do both syscall.Errno identity matching (the canonical path) and a
// cross-platform string fallback because Windows wraps WSA errors in a
// chain that errors.Is doesn't always unwrap to syscall.Errno cleanly. The
// string check is narrow enough to only fire on the well-known transient
// teardown messages, so false positives are highly unlikely.
func isTransientConnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "forcibly closed") || // WSAECONNRESET on Windows
		strings.Contains(msg, "aborted by the software") || // WSAECONNABORTED on Windows
		strings.Contains(msg, "use of closed network connection")
}

func reportAPIErrorsMetric(response *http.Response, err error, endpoint string) {
	if t, ok := getGlobalTracer().(*tracer); ok {
		var reason string
		if err != nil {
			reason = "network_failure"
		}
		if response != nil {
			reason = fmt.Sprintf("server_response_%d", response.StatusCode)
		}
		tags := []string{"reason:" + reason, "endpoint:" + endpoint}
		t.statsd.Incr("datadog.tracer.api.errors", tags, 1)
	} else {
		return
	}
}

func (t *httpTransport) endpoint() string {
	return t.traceURL
}
