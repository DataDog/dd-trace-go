// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package appsec_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	privateAppsec "github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackUserLoginSuccess(t *testing.T) {

	t.Run("nominal-with-metadata", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		appsec.TrackUserLoginSuccess(ctx, "user login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
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
		assertTag(t, finished, "usr.login", "user login")
		assertTag(t, finished, expectedEventPrefix+"region", "us-east-1")
		assertTag(t, finished, "usr.name", "username")

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v2"}]
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
		appsec.TrackUserLoginSuccess(ctx, "user login", "user id", nil)
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]

		sp, _ := finished.Context().SamplingPriority()
		assert.Equal(t, ext.PriorityUserKeep, sp, "span should have user keep (%d) priority (has: %d)", ext.PriorityUserKeep, sp)

		expectedEventPrefix := "appsec.events.users.login.success."
		assertTag(t, finished, expectedEventPrefix+"track", "true")
		assertTag(t, finished, "usr.id", "user id")

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v2"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})

	t.Run("nil-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			//lint:ignore SA1012 we are intentionally passing a nil context to verify incorrect use does not lead to panic
			appsec.TrackUserLoginSuccess(nil, "user login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))

			metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v2"}]
			require.NotNil(t, metric)
			assert.EqualValues(t, 1, metric.Get())
		})
	})

	t.Run("empty-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			appsec.TrackUserLoginSuccess(context.Background(), "user login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))

			metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_success,sdk_version:v2"}]
			require.NotNil(t, metric)
			assert.EqualValues(t, 1, metric.Get())
		})
	})
}

func TestTrackUserLoginFailure(t *testing.T) {
	t.Run("nominal", func(t *testing.T) {
		test := func(userExists bool) func(t *testing.T) {
			return func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				var telemetryRecorder telemetrytest.RecordClient
				restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
				defer restoreTelemetry()

				span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
				appsec.TrackUserLoginFailure(ctx, "user login", userExists, map[string]string{"region": "us-east-1"})
				span.Finish()

				// Check the span contains the expected tags.
				require.Len(t, mt.FinishedSpans(), 1)
				finished := mt.FinishedSpans()[0]

				sp, _ := finished.Context().SamplingPriority()
				assert.Equal(t, ext.PriorityUserKeep, sp, "span should have user keep (%d) priority (has: %d)", ext.PriorityUserKeep, sp)

				expectedEventPrefix := "appsec.events.users.login.failure."
				assertTag(t, finished, "_dd."+expectedEventPrefix+"sdk", "true")
				assertTag(t, finished, expectedEventPrefix+"track", "true")
				assertTag(t, finished, expectedEventPrefix+"usr.login", "user login")
				assertTag(t, finished, expectedEventPrefix+"usr.exists", strconv.FormatBool(userExists))
				assertTag(t, finished, expectedEventPrefix+"region", "us-east-1")

				metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_failure,sdk_version:v2"}]
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
			appsec.TrackUserLoginFailure(nil, "user login", false, nil)

			metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_failure,sdk_version:v2"}]
			require.NotNil(t, metric)
			assert.EqualValues(t, 1, metric.Get())
		})
	})

	t.Run("empty-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			appsec.TrackUserLoginFailure(context.Background(), "user login", false, nil)

			metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:login_failure,sdk_version:v2"}]
			require.NotNil(t, metric)
			assert.EqualValues(t, 1, metric.Get())
		})
	})
}

func TestCustomEvent(t *testing.T) {
	t.Run("nominal", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		md := map[string]string{"key-1": "value 1", "key-2": "value 2", "key-3": "value 3"}
		appsec.TrackCustomEvent(ctx, "my-custom-event", md)
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]

		sp, _ := finished.Context().SamplingPriority()
		assert.Equal(t, ext.PriorityUserKeep, sp, "span should have user keep (%d) priority (has: %d)", ext.PriorityUserKeep, sp)

		expectedEventPrefix := "appsec.events.my-custom-event."
		assertTag(t, finished, expectedEventPrefix+"track", "true")
		for k, v := range md {
			assertTag(t, finished, expectedEventPrefix+k, v)
		}

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:custom,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})

	t.Run("nil-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			//lint:ignore SA1012 we are intentionally passing a nil context to verify incorrect use does not lead to panic
			appsec.TrackCustomEvent(nil, "my-custom-event", nil)
		})

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:custom,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})

	t.Run("empty-context", func(t *testing.T) {
		var telemetryRecorder telemetrytest.RecordClient
		restoreTelemetry := telemetry.MockClient(&telemetryRecorder)
		defer restoreTelemetry()

		require.NotPanics(t, func() {
			appsec.TrackCustomEvent(context.Background(), "my-custom-event", nil)
		})

		metric := telemetryRecorder.Metrics[telemetrytest.MetricKey{Namespace: telemetry.NamespaceAppSec, Name: "sdk.event", Kind: "count", Tags: "event_type:custom,sdk_version:v1"}]
		require.NotNil(t, metric)
		assert.EqualValues(t, 1, metric.Get())
	})
}

func TestSetUser(t *testing.T) {
	t.Run("early-return/appsec-disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		func() {
			span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
			defer span.Finish()
			err := appsec.SetUser(ctx, "usr.id")
			require.NoError(t, err)
		}()

		finished := mt.FinishedSpans()[0]
		assertTag(t, finished, "_dd.appsec.user.collection_mode", "sdk")
	})

	privateAppsec.Start()
	defer privateAppsec.Stop()
	if !privateAppsec.Enabled() {
		t.Skip("AppSec needs to be enabled for this test")
	}

	t.Run("early-return/nil-ctx", func(t *testing.T) {
		//lint:ignore SA1012 we are intentionally passing a nil context to verify incorrect use does not lead to panic
		err := appsec.SetUser(nil, "usr.id")
		require.NoError(t, err)
	})

	t.Run("no-early-return", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		defer span.Finish()
		err := appsec.SetUser(ctx, "usr.id")
		require.Nil(t, err)
	})
}

func ExampleTrackUserLoginSuccess() {
	// Create an example span and set a user login success appsec event example to it.
	span, ctx := tracer.StartSpanFromContext(context.TODO(), "example")
	defer span.Finish()
	appsec.TrackUserLoginSuccess(ctx, "login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
}

func ExampleTrackUserLoginFailure() {
	// Create an example span and set a user login failure appsec event example to it.
	span, ctx := tracer.StartSpanFromContext(context.TODO(), "example")
	defer span.Finish()
	appsec.TrackUserLoginFailure(ctx, "login", false, nil)
}

func ExampleTrackCustomEvent() {
	// Create an example span and set a custom appsec event example to it.
	span, ctx := tracer.StartSpanFromContext(context.TODO(), "example")
	defer span.Finish()
	appsec.TrackCustomEvent(ctx, "my-custom-event", map[string]string{"region": "us-east-1"})

	// To go further in this example, you can add extra security-related context with the authenticated user id when the
	// request is being served for an authenticated user.
	tracer.SetUser(span, "user id")
}
