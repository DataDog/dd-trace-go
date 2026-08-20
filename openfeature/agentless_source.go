// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"compress/gzip"
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// maxPollAttempts is the number of times a single poll retries a retryable
// failure before giving up and waiting for the next tick.
const maxPollAttempts = 3

// maxDrainBytes bounds how much of a non-200 response body is read before
// closing, so the underlying connection can be reused without risking
// unbounded memory use on a misbehaving endpoint.
const maxDrainBytes = 4096

// maxResponseBodyBytes bounds how much of a 200 response body is decoded.
const maxResponseBodyBytes = 10 << 20 // 10MiB

// agentlessSource polls the Agentless configuration endpoint on an interval
// and applies newly received configuration. All fields other than mu, etag,
// and warned are immutable after construction.
type agentlessSource struct {
	// endpoint is the full request URL. SENSITIVE: may embed credentials; never log.
	endpoint string
	// apiKey is sent as DD-API-KEY; empty unless endpoint is the managed one.
	apiKey         string
	pollInterval   time.Duration
	requestTimeout time.Duration
	httpClient     *http.Client
	apply          func(*universalFlagsConfiguration)

	mu     sync.Mutex
	etag   string          // +checklocks:mu
	warned map[string]bool // +checklocks:mu

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// newAgentlessSource builds a poller for settings. It performs no I/O.
func newAgentlessSource(settings internalffe.Settings, apply func(*universalFlagsConfiguration)) (*agentlessSource, error) {
	endpoint, err := buildAgentlessEndpoint(settings.AgentlessBaseURL, settings.Site, settings.Env, settings.APIKey)
	if err != nil {
		return nil, err
	}

	apiKey := ""
	if endpoint.managed {
		apiKey = settings.APIKey
	}

	return &agentlessSource{
		endpoint:       endpoint.url,
		apiKey:         apiKey,
		pollInterval:   settings.PollInterval,
		requestTimeout: settings.RequestTimeout,
		// We build our own transport rather than use http.DefaultTransport so
		// these polls stay out of our own HTTP instrumentation and can never
		// recurse through it (see internal/civisibility/utils/net/http.go).
		httpClient: internal.DefaultHTTPClient(settings.RequestTimeout, false),
		apply:      apply,
		warned:     make(map[string]bool),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}, nil
}

// start issues the first poll synchronously, then hands off to the
// background loop. Checking stopCh first means nothing billable is issued
// once the source has been stopped.
func (s *agentlessSource) start() {
	select {
	case <-s.stopCh:
		close(s.doneCh)
		return
	default:
	}
	s.poll()
	go s.run()
}

// Stop signals the poll loop to exit and waits for it to finish, or for ctx
// to expire.
func (s *agentlessSource) Stop(ctx context.Context) {
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
	case <-ctx.Done():
	}
}

// run is the background poll loop. It uses a Timer reset after each poll
// completes, rather than a Ticker, so a slow poll cannot cause a burst of
// catch-up ticks once it finally returns.
func (s *agentlessSource) run() {
	defer close(s.doneCh)

	timer := time.NewTimer(s.pollInterval)
	defer timer.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-timer.C:
		}

		select {
		case <-s.stopCh:
			return
		default:
		}

		s.poll()
		timer.Reset(s.pollInterval)
	}
}

// poll runs one poll, retrying a retryable failure up to maxPollAttempts
// times with backoff before giving up until the next tick.
func (s *agentlessSource) poll() {
	for attempt := 1; attempt <= maxPollAttempts; attempt++ {
		if s.pollOnce() != pollOutcomeRetryable {
			return
		}
		if attempt == maxPollAttempts {
			return
		}

		delay := retryDelay(s.pollInterval, attempt, rand.Float64())
		select {
		case <-time.After(delay):
		case <-s.stopCh:
			return
		}
	}
}

type pollOutcome int

const (
	// pollOutcomeSuccess covers both a 200 that was parsed and applied, and a
	// 304 that left the current configuration untouched.
	pollOutcomeSuccess pollOutcome = iota
	// pollOutcomeRetryable means the caller should retry within the same poll.
	pollOutcomeRetryable
	// pollOutcomeStop means the failure was handled (logged) and is not
	// expected to be resolved by retrying; keep last-known-good and wait for
	// the next tick.
	pollOutcomeStop
)

// pollOnce issues a single HTTP request and applies its result. Configuration
// is only ever accepted from a 200 response; the ETag only advances after
// both parsing and apply have succeeded, so a malformed payload can never be
// acknowledged as received.
func (s *agentlessSource) pollOnce() pollOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), s.requestTimeout)
	defer cancel()

	// Honor Stop mid-request: cancel the in-flight request rather than
	// waiting out the full request timeout during shutdown.
	requestDone := make(chan struct{})
	defer close(requestDone)
	go func() {
		select {
		case <-s.stopCh:
			cancel()
		case <-requestDone:
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		if s.warnOnce("request") {
			log.Warn("openfeature: agentless: failed to build request: %v", err.Error())
		}
		return pollOutcomeStop
	}
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("DD-Client-Library-Language", "go")
	req.Header.Set("DD-Client-Library-Version", version.Tag)
	if etag := s.currentETag(); etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if s.apiKey != "" {
		req.Header.Set("DD-API-KEY", s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		if s.warnOnce("http") {
			log.Warn("openfeature: agentless: request failed: %v", err.Error())
		}
		return pollOutcomeRetryable
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Never decode a non-200 body; drain a bounded prefix so the
		// connection can be reused.
		_, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes)
	}

	switch resp.StatusCode {
	case http.StatusNotModified:
		return pollOutcomeSuccess
	case http.StatusUnauthorized, http.StatusForbidden:
		if s.warnOnce("authentication") {
			log.Warn("openfeature: agentless: received status %d, verify endpoint authentication", resp.StatusCode)
		}
		return pollOutcomeStop
	case http.StatusOK:
		// handled below
	default:
		if isRetryablePollStatus(resp.StatusCode) {
			if s.warnOnce("http") {
				log.Warn("openfeature: agentless: received retryable status %d", resp.StatusCode)
			}
			return pollOutcomeRetryable
		}
		if s.warnOnce("http") {
			log.Warn("openfeature: agentless: received unexpected status %d", resp.StatusCode)
		}
		return pollOutcomeStop
	}

	body, err := readAgentlessResponseBody(resp)
	if err != nil {
		if s.warnOnce("http") {
			log.Warn("openfeature: agentless: failed to read response body: %v", err.Error())
		}
		return pollOutcomeRetryable
	}

	config, err := parseUFCEnvelope(body)
	if err != nil {
		if s.warnOnce("request") {
			log.Warn("openfeature: agentless: received malformed configuration: %v", err.Error())
		}
		return pollOutcomeStop
	}

	s.apply(config)
	s.setETag(strings.TrimSpace(resp.Header.Get("ETag")))
	return pollOutcomeSuccess
}

func (s *agentlessSource) currentETag() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.etag
}

func (s *agentlessSource) setETag(etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.etag = etag
}

// warnOnce reports whether this is the first time category has fired for the
// lifetime of the source, so a persistently broken endpoint doesn't warn on
// every poll interval forever. Callers must pass a compile-time constant
// format string to the log.Warn call they guard with it — this method only
// returns a bool so linting can still verify that at each call site.
func (s *agentlessSource) warnOnce(category string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	alreadyWarned := s.warned[category]
	s.warned[category] = true
	return !alreadyWarned
}

// readAgentlessResponseBody reads a 200 response body, decoding gzip when
// Content-Encoding says so. Go's transport only auto-decompresses when it set
// Accept-Encoding itself; since we set that header explicitly, decoding is
// our responsibility.
func readAgentlessResponseBody(resp *http.Response) ([]byte, error) {
	reader := io.Reader(resp.Body)
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}
	return io.ReadAll(io.LimitReader(reader, maxResponseBodyBytes))
}

// isRetryablePollStatus reports whether status warrants retrying within the
// same poll: a transport error/timeout (represented as status 0), 408, 429,
// or any 5xx. Any other status is either terminal success (2xx/304) or a
// problem retrying cannot fix (other 4xx).
func isRetryablePollStatus(status int) bool {
	if status == 0 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status <= 599
}

// retryDelay computes the backoff before retrying attempt within one poll.
// random must be in [0, 1); callers pass rand.Float64() so it is deterministic
// in tests. The clamps already impose 2s/5s minimums, so the final 1ms floor
// is unreachable in practice; it exists only to match dd-trace-js's
// Math.max(1, ...) floor rather than leaving delay unbounded below zero.
func retryDelay(pollInterval time.Duration, attempt int, random float64) time.Duration {
	var base time.Duration
	if attempt <= 1 {
		base = clampDuration(pollInterval/6, 2*time.Second, 10*time.Second)
	} else {
		base = clampDuration(pollInterval/3, 5*time.Second, 30*time.Second)
	}

	jitter := 0.8 + random*0.4 // +/- 20%
	delay := time.Duration(float64(base) * jitter)
	return max(delay, time.Millisecond)
}

func clampDuration(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}
