// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package appsec_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackUserLoginSuccessEvent(t *testing.T) {
	t.Run("nominal-with-metadata", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		appsec.TrackUserLoginSuccessEvent(ctx, "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]

		sp, _ := finished.Context().SamplingPriority()
		assert.Equal(t, ext.PriorityUserKeep, sp, "span should have user keep (%d) priority (has: %d)", ext.PriorityUserKeep, sp)

		expectedEventPrefix := "appsec.events.users.login.success."
		assertTag(t, finished, "_dd."+expectedEventPrefix+"sdk", "true")
		assertTag(t, finished, expectedEventPrefix+"track", "true")
		assertTag(t, finished, "usr.id", "user id")
		assertTag(t, finished, expectedEventPrefix+"region", "us-east-1")
		assertTag(t, finished, "usr.name", "username")

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})

	t.Run("nominal-nil-metadata", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		appsec.TrackUserLoginSuccessEvent(ctx, "user id", nil)
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]

		sp, _ := finished.Context().SamplingPriority()
		assert.Equal(t, ext.PriorityUserKeep, sp, "span should have user keep (%d) priority (has: %d)", ext.PriorityUserKeep, sp)

		expectedEventPrefix := "appsec.events.users.login.success."
		assertTag(t, finished, expectedEventPrefix+"track", "true")
		assertTag(t, finished, "usr.id", "user id")

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})

	t.Run("nil-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			//lint:ignore SA1012 we are intentionally passing a nil context to verify incorrect use does not lead to panic
			appsec.TrackUserLoginSuccessEvent(nil, "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		})

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})

	t.Run("empty-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			appsec.TrackUserLoginSuccessEvent(context.Background(), "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		})

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})
}

func TestTrackUserLoginFailureEvent(t *testing.T) {
	t.Run("nominal", func(t *testing.T) {
		test := func(userExists bool) func(t *testing.T) {
			return func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				var telemetryRecorder telemetrytest.RecordClient
				restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
				defer restoreTelemetry()

				span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
				appsec.TrackUserLoginFailureEvent(ctx, "user id", userExists, map[string]string{"region": "us-east-1"})
				span.Finish()

				// Check the span contains the expected tags.
				require.Len(t, mt.FinishedSpans(), 1)
				finished := mt.FinishedSpans()[0]

				sp, _ := finished.Context().SamplingPriority()
				assert.Equal(t, ext.PriorityUserKeep, sp, "span should have user keep (%d) priority (has: %d)", ext.PriorityUserKeep, sp)

				expectedEventPrefix := "appsec.events.users.login.failure."
				assertTag(t, finished, "_dd."+expectedEventPrefix+"sdk", "true")
				assertTag(t, finished, expectedEventPrefix+"track", "true")
				assertTag(t, finished, expectedEventPrefix+"usr.id", "user id")
				assertTag(t, finished, expectedEventPrefix+"usr.exists", strconv.FormatBool(userExists))
				assertTag(t, finished, expectedEventPrefix+"region", "us-east-1")

				metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_failure,sdk_version:v1"}]
				require.NotNil(t, metric)
				assert.EqualValues(t, 1, metric.Get())
			}
		}
		t.Run("user-exists", test(true))
		t.Run("user-not-exists", test(false))
	})

	t.Run("nil-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			//lint:ignore SA1012 we are intentionally passing a nil context to verify incorrect use does not lead to panic
			appsec.TrackUserLoginFailureEvent(nil, "user id", false, nil)
		})

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_failure,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})

	t.Run("empty-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			appsec.TrackUserLoginFailureEvent(context.Background(), "user id", false, nil)
		})

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_failure,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})
}

func assertTag(t *testing.T, span *mocktracer.Span, tag string, value any) bool {
	return assert.EqualValues(t, value, span.Tag(tag), "span tag %q should have value %#v", tag, value)
}
