// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command staleidle-soak is a load driver for verifying the APMS-19533 UDS
// stale-idle connection fix end-to-end against a real Datadog Agent. It is
// intentionally lightweight: it does NOT serve HTTP, it does not need real
// Datadog backend access, and it talks to the agent over a UDS socket
// bind-mounted from a docker-compose-managed agent container.
//
// The driver creates spans concurrently for a fixed duration, captures the
// tracer's statsd metrics (via a local UDP listener configured with
// WithDogstatsdAddr) and the tracer's error logs (via WithLogger), and emits a
// single JSON result line on stdout when finished. The orchestrator script
// (soak.sh) consumes that JSON line for A/B comparison between the baseline
// commit and the fix branch.
//
// All driver-side configuration is via CLI flags so the same binary can be
// rebuilt against either branch and produce comparable output. There are no
// build tags.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	var (
		udsPath     = flag.String("uds-path", "/tmp/staleidle-uds/apm.socket", "path to the agent's UDS socket")
		duration    = flag.Duration("duration", 60*time.Second, "how long to drive load")
		concurrency = flag.Int("concurrency", 50, "number of concurrent goroutines creating spans")
		spanRate    = flag.Int("spans-per-sec", 100, "per-goroutine target span creation rate")
		label       = flag.String("label", "patched", "result label (baseline / patched)")
	)
	flag.Parse()

	// Some Datadog dev environments set OTEL_* env vars (Claude Code
	// workspaces, certain CI runners). If those leak into the tracer, the
	// trace writer switches to OTLP export mode and our traces never reach
	// the agent over UDS — which silently invalidates the entire soak run.
	// Unset the relevant ones up front. This only affects the current process.
	for _, k := range []string{
		"OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_LOGS_EXPORTER",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_METRICS_INCLUDE_VERSION", "OTEL_METRICS_TEMPORALITY_PREFERENCE",
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
	} {
		os.Unsetenv(k)
	}

	// Capture customer-visible error log lines. These are the exact strings
	// that appear in the customer's Datadog log search.
	logCap := newLogCapture()
	defer logCap.dump()

	// Spin up a UDP statsd sink so we can intercept the tracer's metrics
	// counters in-process. The tracer fires these via datadog-go/v5; the wire
	// format is plain text statsd packets we can parse trivially.
	sink, statsdAddr, err := startStatsdSink()
	if err != nil {
		fail("statsd sink: %v", err)
	}

	tracer.Start(
		tracer.WithUDS(*udsPath),
		tracer.WithDogstatsdAddr(statsdAddr),
		tracer.WithLogger(logCap),
		tracer.WithLogStartup(false),
		tracer.WithService("staleidle-soak"),
		tracer.WithEnv("dev"),
	)

	// Drive load: <concurrency> goroutines, each generating spans at the
	// target rate for <duration>. The work inside each span is intentionally
	// trivial — the bug we're verifying is in the transport layer, not in
	// any user code path.
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var spansCreated atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()

	for range *concurrency {
		wg.Go(func() {
			tick := time.NewTicker(time.Second / time.Duration(*spanRate))
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					span := tracer.StartSpan("staleidle.soak.op")
					span.SetTag("worker", "soak")
					// Force manual keep so each span survives priority
					// sampling and reaches the writer / transport. Without
					// this the tracer drops P0 traces locally (because the
					// agent advertises client_drop_p0s) and the transport
					// layer — which is what we're trying to exercise — never
					// runs.
					span.SetTag(ext.ManualKeep, true)
					span.Finish()
					spansCreated.Add(1)
				}
			}
		})
	}
	wg.Wait()
	driveElapsed := time.Since(start)

	// tracer.Stop blocks until all pending flushes have been attempted.
	// After it returns, statsd packets for the final flush may still be in
	// flight on the local UDP loopback; give them a moment to land.
	tracer.Stop()
	time.Sleep(500 * time.Millisecond)
	sink.close()

	// Emit a single JSON line on stdout. soak.sh redirects this into the
	// per-scenario results file.
	result := map[string]any{
		"label":                    *label,
		"duration_s":               driveElapsed.Seconds(),
		"concurrency":              *concurrency,
		"target_spans_per_sec":     *spanRate * *concurrency,
		"spans_created":            spansCreated.Load(),
		"uds_path":                 *udsPath,
		"api_errors":               sink.count("datadog.tracer.api.errors"),
		"api_errors_by_endpoint":   sink.tagBreakdown("datadog.tracer.api.errors", "endpoint:"),
		"api_errors_by_reason":     sink.tagBreakdown("datadog.tracer.api.errors", "reason:"),
		"flush_traces":             sink.count("datadog.tracer.flush_traces"),
		"flush_bytes":              sink.count("datadog.tracer.flush_bytes"),
		"traces_dropped":           sink.count("datadog.tracer.traces_dropped"),
		"traces_dropped_by_reason": sink.tagBreakdown("datadog.tracer.traces_dropped", "reason:"),
		"stats_flush_payloads":     sink.count("datadog.tracer.stats.flush_payloads"),
		"stats_flush_errors":       sink.count("datadog.tracer.stats.flush_errors"),
		"lost_trace_log_count":     logCap.count("lost ", " traces:"),
		"send_stats_err_log_count": logCap.count("Error sending stats payload"),
		"all_error_logs":           logCap.errors(),
		"_all_counters":            sink.allCounters(),
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail("encode result: %v", err)
	}
}

// ---------------- log capture ----------------

// logCapture implements tracer.Logger. It records every log line so we can
// count the specific customer-visible failure messages and inspect anything
// unexpected at the end of the run.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func newLogCapture() *logCapture { return &logCapture{} }

func (l *logCapture) Log(msg string) {
	l.mu.Lock()
	l.lines = append(l.lines, msg)
	l.mu.Unlock()
}

func (l *logCapture) count(parts ...string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		all := true
		for _, p := range parts {
			if !strings.Contains(line, p) {
				all = false
				break
			}
		}
		if all {
			n++
		}
	}
	return n
}

// errors returns all log lines that look like ERROR-level output. This is the
// debug payload for failed runs — when a scenario produces non-zero error
// counts we want the operator to see what the tracer actually said.
func (l *logCapture) errors() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range l.lines {
		if strings.Contains(line, "ERROR") {
			out = append(out, line)
		}
	}
	return out
}

func (l *logCapture) dump() {
	// No-op in normal operation; reserved for future debugging hooks.
}

// ---------------- statsd capture ----------------

// statsdSink listens on a UDP loopback port for the tracer's statsd packets
// and accumulates counter increments. It parses only the simple
// "<metric>:<value>|c|#tag1,tag2" form — enough for our tracer counters. The
// tracer also emits gauges, histograms, and timings, which we deliberately
// ignore here; the bug we're verifying surfaces as counter movement.
type statsdSink struct {
	conn  *net.UDPConn
	mu    sync.Mutex
	hits  map[string]float64            // metric -> total
	byTag map[string]map[string]float64 // metric -> tagPrefix+value -> total (per prefix request)
}

func startStatsdSink() (*statsdSink, string, error) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, "", err
	}
	s := &statsdSink{
		conn:  conn,
		hits:  make(map[string]float64),
		byTag: make(map[string]map[string]float64),
	}
	go s.run()
	return s, conn.LocalAddr().String(), nil
}

func (s *statsdSink) run() {
	buf := make([]byte, 65536)
	for {
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		s.handle(buf[:n])
	}
}

func (s *statsdSink) handle(packet []byte) {
	scanner := bufio.NewScanner(strings.NewReader(string(packet)))
	for scanner.Scan() {
		line := scanner.Text()
		// DogStatsD format:
		//   <METRIC>:<VALUE>|<TYPE>[|@<SAMPLE_RATE>][|#<TAG>,<TAG>...][|c:<CID>][|T<TS>]
		// We accept all metric types here (counters / gauges / timings /
		// histograms) and only record the value verbatim; the caller decides
		// what to do with it.
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		metric := line[:colon]
		rest := line[colon+1:]

		// Split on '|'. Field 0 is the value; field 1 is the type; later
		// fields may include @<rate>, #<tags>, c:<container>, T<timestamp>.
		fields := strings.Split(rest, "|")
		if len(fields) < 2 {
			continue
		}
		var val float64
		if _, err := fmt.Sscanf(fields[0], "%f", &val); err != nil {
			continue
		}

		// Extract just the tag list (the field that starts with '#'), if any.
		// Importantly, tags do NOT include the trailing c:<container> or
		// T<timestamp> segments — those are separate '|'-delimited fields.
		var tags []string
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "#") {
				tags = strings.Split(f[1:], ",")
				break
			}
		}

		s.mu.Lock()
		s.hits[metric] += val
		bucket, ok := s.byTag[metric]
		if !ok {
			bucket = make(map[string]float64)
			s.byTag[metric] = bucket
		}
		for _, tag := range tags {
			bucket[tag] += val
		}
		s.mu.Unlock()
	}
}

// allCounters returns a snapshot of every metric the sink has observed.
// Useful for debugging when expected counters appear absent.
func (s *statsdSink) allCounters() map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]float64, len(s.hits))
	for k, v := range s.hits {
		out[k] = v
	}
	return out
}

func (s *statsdSink) count(metric string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[metric]
}

func (s *statsdSink) tagBreakdown(metric, tagPrefix string) map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]float64{}
	for tag, v := range s.byTag[metric] {
		if strings.HasPrefix(tag, tagPrefix) {
			out[tag] += v
		}
	}
	return out
}

func (s *statsdSink) close() {
	_ = s.conn.Close()
}

// ---------------- helpers ----------------

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "staleidle-soak: "+format+"\n", args...)
	os.Exit(1)
}
