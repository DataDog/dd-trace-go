// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpanPoolActivationConfig pins the env var / StartOption precedence for
// the experimental span pool. span_pool_test.go only ever turns pooling on
// via WithSpanPool(true); this covers how the flag resolves before any of
// that behavioral coverage applies.
func TestSpanPoolActivationConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string // "" means unset
		opts []StartOption
		want bool
	}{
		{name: "unset", want: false},
		{name: "env true", env: "true", want: true},
		{name: "env TRUE", env: "TRUE", want: true},
		{name: "env 1", env: "1", want: true},
		{name: "env t", env: "t", want: true},
		{name: "env false", env: "false", want: false},
		{name: "env 0", env: "0", want: false},
		{name: "env garbage falls back to default", env: "notabool", want: false},
		{name: "env empty is treated as unset", env: "", want: false},
		{name: "option true, no env", opts: []StartOption{WithSpanPool(true)}, want: true},
		{name: "option false, no env", opts: []StartOption{WithSpanPool(false)}, want: false},
		{name: "option true overrides env false", env: "false", opts: []StartOption{WithSpanPool(true)}, want: true},
		{name: "option false overrides env true", env: "true", opts: []StartOption{WithSpanPool(false)}, want: false},
		{name: "last option wins", opts: []StartOption{WithSpanPool(true), WithSpanPool(false)}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Always set (never skip): the invoking environment may already have
			// this var set, and tc.env's zero value ("") is itself the "unset"
			// case per the provider's semantics, so an unconditional Setenv keeps
			// every case deterministic regardless of the ambient shell/CI env.
			t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", tc.env)
			cfg, err := newTestConfig(tc.opts...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.internalConfig.SpanPoolEnabled())
		})
	}
}

// latestConfig returns the telemetry.Configuration entry with the highest
// SeqID for name — the entry that reflects the value actually held by the
// config, since later Report/ReportWithID calls win over earlier ones.
func latestConfig(t *testing.T, cfgs []telemetry.Configuration, name string) telemetry.Configuration {
	t.Helper()
	var found *telemetry.Configuration
	for i, c := range cfgs {
		if c.Name == name && (found == nil || c.SeqID > found.SeqID) {
			found = &cfgs[i]
		}
	}
	require.NotNil(t, found, "no telemetry config reported for %s", name)
	return *found
}

// TestSpanPoolConfigTelemetry asserts not just that a value is reported, but
// which source won: the default, the raw env var string, or the StartOption
// (as a bool, via SetSpanPoolEnabled). The "contradicting" case additionally
// covers env and option disagreeing at the same time: both reports must
// exist (the losing source is still recorded, per configtelemetry's design),
// but the option's entry must be the one with the highest SeqID.
func TestSpanPoolConfigTelemetry(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		// Force the var unset regardless of the invoking environment: an
		// inherited "true" would report OriginEnvVar instead of OriginDefault
		// and break this assertion.
		t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "")
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		_, err := newTestConfig()
		require.NoError(t, err)

		got := latestConfig(t, telemetryClient.Configuration, "DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED")
		assert.Equal(t, telemetry.OriginDefault, got.Origin)
		assert.Equal(t, false, got.Value)
	})

	t.Run("env var", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()
		t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "true")

		_, err := newTestConfig()
		require.NoError(t, err)

		got := latestConfig(t, telemetryClient.Configuration, "DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED")
		assert.Equal(t, telemetry.OriginEnvVar, got.Origin)
		// The provider reports the raw string value, not the parsed bool.
		assert.Equal(t, "true", got.Value)
	})

	t.Run("option", func(t *testing.T) {
		// Unset too: the option wins over any env value regardless (proven by
		// the "contradicting" case below), but pinning the starting state keeps
		// this subtest from silently depending on that fact.
		t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "")
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		_, err := newTestConfig(WithSpanPool(true))
		require.NoError(t, err)

		got := latestConfig(t, telemetryClient.Configuration, "DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED")
		assert.Equal(t, telemetry.OriginCode, got.Origin)
		assert.Equal(t, true, got.Value)
	})

	t.Run("contradicting env and option, option wins", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()
		t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "false")

		_, err := newTestConfig(WithSpanPool(true))
		require.NoError(t, err)

		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "false")
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", true)

		got := latestConfig(t, telemetryClient.Configuration, "DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED")
		assert.Equal(t, telemetry.OriginCode, got.Origin)
		assert.Equal(t, true, got.Value)
	})
}

// TestSpanPoolSnapshotTracksConfig guards the asymmetry between the two read
// sites: acquireSpan reads SpanStartSnapshot().SpanPoolEnabled (tracer.go),
// while releaseSpans reads the live Config.SpanPoolEnabled() (tracer.go). If
// the snapshot ever stops carrying the field, spans would be acquired
// unpooled but released into the pool (or vice versa).
func TestSpanPoolSnapshotTracksConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []StartOption
	}{
		{name: "disabled"},
		{name: "enabled", opts: []StartOption{WithSpanPool(true)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := newTestConfig(tc.opts...)
			require.NoError(t, err)
			assert.Equal(t,
				cfg.internalConfig.SpanPoolEnabled(),
				cfg.internalConfig.SpanStartSnapshot().SpanPoolEnabled,
			)
		})
	}
}

// TestSpanPoolActivationReachesHotPath is the deterministic proof that the
// resolved flag actually reaches acquireSpan/releaseSpans, not just that the
// config value is correct. releaseSpans is the only caller of Span.clear(),
// so a finished span left un-cleared after a completed flush means the flag
// never reached the release path. The "contradicting" cases go one step
// further than TestSpanPoolActivationConfig: it is not enough for
// SpanPoolEnabled() to resolve to the option's value when env and option
// disagree — the actual pooling behavior must follow it too.
func TestSpanPoolActivationReachesHotPath(t *testing.T) {
	for _, tc := range []struct {
		name       string
		env        string
		opts       []StartOption
		wantPooled bool
	}{
		{name: "env only", env: "true", wantPooled: true},
		{name: "option only", opts: []StartOption{WithSpanPool(true)}, wantPooled: true},
		{name: "disabled", wantPooled: false},
		{name: "option true overrides env false", env: "false", opts: []StartOption{WithSpanPool(true)}, wantPooled: true},
		{name: "option false overrides env true", env: "true", opts: []StartOption{WithSpanPool(false)}, wantPooled: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Always set, same reasoning as TestSpanPoolActivationConfig: an
			// inherited ambient value must not leak into the "disabled" case.
			t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", tc.env)
			tr, transport, flush, stop, err := startTestTracer(t, tc.opts...)
			require.NoError(t, err)
			defer stop()

			s := tr.StartSpan("op", ServiceName("svc"), ResourceName("/r"), Tag(ext.ManualKeep, true))
			s.Finish()
			flush(1)
			require.Len(t, transport.Traces(), 1)

			if tc.wantPooled {
				assert.Empty(t, s.name, "releaseSpans should have cleared the span")
				assert.Zero(t, s.spanID)
			} else {
				assert.Equal(t, "op", s.name, "span must not be cleared when pooling is off")
			}
		})
	}
}

// TestSpanPoolUnaffectedByStatsComputationAndDataStreams is a regression
// guard for a reported production symptom: a service set
// DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED=true alongside
// DD_TRACE_STATS_COMPUTATION_ENABLED=false and DD_DATA_STREAMS_ENABLED=true,
// and observed the trace protocol downgrade to v0.4 — a real, intentional
// consequence of disabling stats computation (see newConfig: "v1 is not
// compatible without CSS") — and suspected the span pool was disabled too.
// It wasn't, and can't be by this config: SetSpanPoolEnabled has exactly two
// call sites in the whole repo (the env-var load in internal/config and
// WithSpanPool in this package's newConfig), and neither references stats
// computation, trace protocol, or DSM. There is now no mechanism that forces
// pooling off behind the caller's back: the Orchestrion incompatibility gate
// that used to be the one exception is gone, since the GLS liveness bit moved
// off the span onto the stack entry and recycling can no longer strand it. The
// woven build asserts that directly in
// internal/orchestrion/_integration/gls/span_pool_test.go.
func TestSpanPoolUnaffectedByStatsComputationAndDataStreams(t *testing.T) {
	t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "true")
	t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "false")
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")

	tr, transport, flush, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	require.True(t, tr.config.internalConfig.SpanPoolEnabled())
	assert.False(t, tr.config.canComputeStats(), "sanity check: stats computation must actually be off")

	s := tr.StartSpan("op", ServiceName("svc"), ResourceName("/r"), Tag(ext.ManualKeep, true))
	s.Finish()
	flush(1)
	require.Len(t, transport.Traces(), 1)
	assert.Empty(t, s.name, "span pool must recycle spans regardless of stats computation / DSM config")
}

// TestSpanPoolNotUsedWhenTracingDisabled confirms DD_TRACE_ENABLED=false short
// circuits StartSpan before the pool is ever touched, even if the pool env
// var resolves to true.
func TestSpanPoolNotUsedWhenTracingDisabled(t *testing.T) {
	t.Setenv("DD_TRACE_ENABLED", "false")
	t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "true")

	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	assert.True(t, tr.config.internalConfig.SpanPoolEnabled())
	assert.Nil(t, tr.StartSpan("op"))
}

// TestSpanPoolProgrammaticOverrideNotInheritedByNextStart pins that
// newConfig's internalconfig.CreateNew() rebuilds config from scratch, so a
// WithSpanPool(true) passed to one tracer.Start does not leak into a later
// Start that omits it.
func TestSpanPoolProgrammaticOverrideNotInheritedByNextStart(t *testing.T) {
	// cfg2 asserts a hardcoded false below, so the env var must be pinned
	// unset regardless of the invoking environment.
	t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "")

	cfg1, err := newTestConfig(WithSpanPool(true))
	require.NoError(t, err)
	assert.True(t, cfg1.internalConfig.SpanPoolEnabled())

	cfg2, err := newTestConfig()
	require.NoError(t, err)
	assert.False(t, cfg2.internalConfig.SpanPoolEnabled())
}
