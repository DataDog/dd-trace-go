// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otlpjson

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// Retry settings, the same as the ones used for the upstream HTTP exporters.
const (
	// Start with 1s to recover quickly from transient failures.
	defaultInitialInterval = 1 * time.Second
	// Grow the wait by 1.5x after each failure.
	backoffMultiplier = 1.5
	// Cap each wait at 30s to avoid excessive delays.
	defaultMaxInterval = 30 * time.Second
	// Give up after 5 minutes to prevent indefinite hangs.
	defaultMaxElapsedTime = 5 * time.Minute

	// maxResponseBytes bounds how much of a response body is read.
	maxResponseBytes = 1 << 20
)

// SenderConfig configures a Sender.
type SenderConfig struct {
	// URL is the complete endpoint, for example http://localhost:4318/v1/logs.
	URL string
	// Headers are added to every request.
	Headers map[string]string
	// Timeout is the deadline of one Send call, retries included.
	Timeout time.Duration
	// Gzip compresses the body and sets Content-Encoding: gzip.
	Gzip bool
	// Signal names the signal ("logs" or "metrics") in messages.
	Signal string
	// RejectedField is the partialSuccess member counting rejected items
	// ("rejectedLogRecords" or "rejectedDataPoints").
	RejectedField string
}

// Sender POSTs OTLP/JSON bodies, retrying the failures the OTLP spec marks as retryable.
type Sender struct {
	cfg    SenderConfig
	client *http.Client

	initialInterval time.Duration
	maxInterval     time.Duration
	maxElapsedTime  time.Duration
}

// Close releases the idle connections of the underlying HTTP client.
func (s *Sender) Close() { s.client.CloseIdleConnections() }

// NewSender returns a Sender using dd-trace-go's default HTTP client settings:
// proxy from the environment and the system trust store.
func NewSender(cfg SenderConfig) *Sender {
	return &Sender{
		cfg:             cfg,
		client:          internal.DefaultHTTPClient(cfg.Timeout, false),
		initialInterval: defaultInitialInterval,
		maxInterval:     defaultMaxInterval,
		maxElapsedTime:  defaultMaxElapsedTime,
	}
}

// Send posts body, an encoded OTLP/JSON request. It retries on 429, 502, 503
// and 504 and on temporary or timeout network errors, with randomized exponential backoff, and
// honors Retry-After. Timeout bounds the whole call.
func (s *Sender) Send(ctx context.Context, body []byte) error {
	if s.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
		defer cancel()
	}
	payload := body
	if s.cfg.Gzip {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(body); err != nil {
			return fmt.Errorf("compressing OTLP %s request: %w", s.cfg.Signal, err)
		}
		if err := zw.Close(); err != nil {
			return fmt.Errorf("compressing OTLP %s request: %w", s.cfg.Signal, err)
		}
		payload = buf.Bytes()
	}

	start := time.Now()
	interval := s.initialInterval
	for {
		retryAfter, err := s.attempt(ctx, payload)
		if err == nil {
			return nil
		}
		var re *retryableError
		if !errors.As(err, &re) {
			return err
		}
		// Randomize the wait by +-50% so exporters that failed together do not retry together.
		wait := time.Duration(float64(interval) * (0.5 + rand.Float64()))
		// A server-requested delay never shortens the backoff.
		wait = max(wait, retryAfter)
		if time.Since(start)+wait > s.maxElapsedTime {
			return fmt.Errorf("giving up sending OTLP %s after %s: %w", s.cfg.Signal, s.maxElapsedTime, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("sending OTLP %s: %w (last error: %v)", s.cfg.Signal, ctx.Err(), err)
		case <-time.After(wait):
		}
		interval = min(time.Duration(float64(interval)*backoffMultiplier), s.maxInterval)
	}
}

// retryableError marks a failure worth retrying.
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// attempt sends payload once. A non-nil retryAfter is the server-requested wait.
func (s *Sender) attempt(ctx context.Context, payload []byte) (retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("creating OTLP %s request: %w", s.cfg.Signal, err)
	}
	req.Header.Set("User-Agent", "dd-trace-go/"+version.Tag)
	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.Gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("sending OTLP %s: %w", s.cfg.Signal, err)
		}
		err = fmt.Errorf("sending OTLP %s: %w", s.cfg.Signal, err)
		// Like the upstream exporters, only temporary failures are retried: a refused
		// connection or an unresolvable host fails fast instead of blocking the exporter.
		var netErr net.Error
		if errors.As(err, &netErr) && (netErr.Timeout() || isTemporary(netErr)) {
			return 0, &retryableError{err}
		}
		return 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		s.logPartialSuccess(respBody)
		return 0, nil
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusBadGateway,
		resp.StatusCode == http.StatusServiceUnavailable,
		resp.StatusCode == http.StatusGatewayTimeout:
		err := fmt.Errorf("OTLP %s endpoint returned %s", s.cfg.Signal, resp.Status)
		return parseRetryAfter(resp.Header.Get("Retry-After")), &retryableError{err}
	default:
		return 0, fmt.Errorf("OTLP %s endpoint returned %s: %s", s.cfg.Signal, resp.Status, truncate(string(respBody), 256))
	}
}

// isTemporary reports whether err advertises itself as temporary. net.Error.Temporary
// is deprecated, but it is what the upstream exporters rely on to decide on retries.
func isTemporary(err error) bool {
	t, ok := err.(interface{ Temporary() bool })
	return ok && t.Temporary()
}

// parseRetryAfter reads a Retry-After header holding seconds or an HTTP date; 0 means absent or invalid.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, time.Until(t))
	}
	return 0
}

// logPartialSuccess warns when the server accepted the request but rejected some items.
// A body that is not a valid response is ignored.
func (s *Sender) logPartialSuccess(body []byte) {
	if len(body) == 0 {
		return
	}
	var resp struct {
		PartialSuccess map[string]json.RawMessage `json:"partialSuccess"`
	}
	if json.Unmarshal(body, &resp) != nil || resp.PartialSuccess == nil {
		return
	}
	// 64-bit counters are strings in OTLP/JSON, but servers may send numbers.
	rejected := strings.Trim(string(resp.PartialSuccess[s.cfg.RejectedField]), `"`)
	var msg string
	_ = json.Unmarshal(resp.PartialSuccess["errorMessage"], &msg)
	if (rejected == "" || rejected == "0") && msg == "" {
		return
	}
	log.Warn("OTLP %s export was partially rejected by the server: rejected=%q message=%q", s.cfg.Signal, rejected, truncate(msg, 256)) //errtrack:ignore server-reported rejection
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
