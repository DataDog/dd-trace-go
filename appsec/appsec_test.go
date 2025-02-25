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

	"github.com/stretchr/testify/require"
)

func TestTrackUserLoginSuccess(t *testing.T) {
	t.Run("nominal-with-metadata", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		appsec.TrackUserLoginSuccess(ctx, "user login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]
		expectedEventPrefix := "appsec.events.users.login.success."
		require.Equal(t, "true", finished.Tag("_dd."+expectedEventPrefix+"sdk"))
		require.Equal(t, "true", finished.Tag(expectedEventPrefix+"track"))
		sp, _ := finished.Context().SamplingPriority()
		require.Equal(t, "true", finished.Tag("_dd."+expectedEventPrefix+"sdk"))
		require.Equal(t, "true", finished.Tag(expectedEventPrefix+"track"))
		require.Equal(t, ext.PriorityUserKeep, sp)
		require.Equal(t, "user id", finished.Tag("usr.id"))
		require.Equal(t, "us-east-1", finished.Tag(expectedEventPrefix+"region"))
		require.Equal(t, "username", finished.Tag("usr.name"))
	})

	t.Run("nominal-nil-metadata", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		appsec.TrackUserLoginSuccess(ctx, "user login", "user id", nil)
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]
		expectedEventPrefix := "appsec.events.users.login.success."
		require.Equal(t, "true", finished.Tag(expectedEventPrefix+"track"))
		sp, _ := finished.Context().SamplingPriority()
		require.Equal(t, ext.PriorityUserKeep, sp)
		require.Equal(t, "user id", finished.Tag("usr.id"))
	})

	t.Run("nil-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginSuccess(nil, "user login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		})
	})

	t.Run("empty-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginSuccess(context.Background(), "user login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		})
	})
}

func TestTrackUserLoginFailure(t *testing.T) {
	t.Run("nominal", func(t *testing.T) {
		test := func(userExists bool) func(t *testing.T) {
			return func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
				appsec.TrackUserLoginFailure(ctx, "user login", userExists, map[string]string{"region": "us-east-1"})
				span.Finish()

				// Check the span contains the expected tags.
				require.Len(t, mt.FinishedSpans(), 1)
				finished := mt.FinishedSpans()[0]
				expectedEventPrefix := "appsec.events.users.login.failure."
				sp, _ := finished.Context().SamplingPriority()
				require.Equal(t, "true", finished.Tag("_dd."+expectedEventPrefix+"sdk"))
				require.Equal(t, "true", finished.Tag(expectedEventPrefix+"track"))
				require.Equal(t, ext.PriorityUserKeep, sp)
				require.Equal(t, "user id", finished.Tag(expectedEventPrefix+"usr.id"))
				require.Equal(t, strconv.FormatBool(userExists), finished.Tag(expectedEventPrefix+"usr.exists"))
				require.Equal(t, "us-east-1", finished.Tag(expectedEventPrefix+"region"))
			}
		}
		t.Run("user-exists", test(true))
		t.Run("user-not-exists", test(false))
	})

	t.Run("nil-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginFailure(nil, "user login", false, nil)
		})
	})

	t.Run("empty-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginFailure(context.Background(), "user login", false, nil)
		})
	})
}

func TestCustomEvent(t *testing.T) {
	t.Run("nominal", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		md := map[string]string{"key-1": "value 1", "key-2": "value 2", "key-3": "value 3"}
		appsec.TrackCustomEvent(ctx, "my-custom-event", md)
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]
		expectedEventPrefix := "appsec.events.my-custom-event."
		require.Equal(t, "true", finished.Tag(expectedEventPrefix+"track"))
		sp, _ := finished.Context().SamplingPriority()
		require.Equal(t, ext.PriorityUserKeep, sp)
		for k, v := range md {
			require.Equal(t, v, finished.Tag(expectedEventPrefix+k))
		}
	})

	t.Run("nil-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackCustomEvent(nil, "my-custom-event", nil)
		})
	})

	t.Run("empty-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackCustomEvent(context.Background(), "my-custom-event", nil)
		})
	})
}

func TestSetUser(t *testing.T) {
	t.Run("early-return/appsec-disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		defer span.Finish()
		err := appsec.SetUser(ctx, "usr.id")
		require.NoError(t, err)
	})

	privateAppsec.Start()
	defer privateAppsec.Stop()
	if !privateAppsec.Enabled() {
		t.Skip("AppSec needs to be enabled for this test")
	}

	t.Run("early-return/nil-ctx", func(t *testing.T) {
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
	span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
	defer span.Finish()
	appsec.TrackUserLoginSuccess(ctx, "login", "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
}

func ExampleTrackUserLoginFailure() {
	// Create an example span and set a user login failure appsec event example to it.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
	defer span.Finish()
	appsec.TrackUserLoginFailure(ctx, "login", false, nil)
}

func ExampleTrackCustomEvent() {
	// Create an example span and set a custom appsec event example to it.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
	defer span.Finish()
	appsec.TrackCustomEvent(ctx, "my-custom-event", map[string]string{"region": "us-east-1"})

	// To go further in this example, you can add extra security-related context with the authenticated user id when the
	// request is being served for an authenticated user.
	tracer.SetUser(span, "user id")
}
