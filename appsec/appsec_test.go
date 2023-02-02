// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package appsec_test

import (
	"context"
	privateAppsec "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"testing"

	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestTrackUserLoginSuccessEvent(t *testing.T) {
	t.Run("nominal-with-metadata", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		appsec.TrackUserLoginSuccessEvent(ctx, "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]
		expectedEventPrefix := "appsec.events.users.login.success."
		require.Equal(t, true, finished.Tag(expectedEventPrefix+"track"))
		require.Equal(t, "user id", finished.Tag("usr.id"))
		require.Equal(t, "us-east-1", finished.Tag(expectedEventPrefix+"region"))
		require.Equal(t, "username", finished.Tag("usr.name"))
	})

	t.Run("nominal-nil-metadata", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		appsec.TrackUserLoginSuccessEvent(ctx, "user id", nil)
		span.Finish()

		// Check the span contains the expected tags.
		require.Len(t, mt.FinishedSpans(), 1)
		finished := mt.FinishedSpans()[0]
		expectedEventPrefix := "appsec.events.users.login.success."
		require.Equal(t, true, finished.Tag(expectedEventPrefix+"track"))
		require.Equal(t, "user id", finished.Tag("usr.id"))
	})

	t.Run("nil-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginSuccessEvent(nil, "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		})
	})

	t.Run("empty-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginSuccessEvent(context.Background(), "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
		})
	})
}

func TestTrackUserLoginFailureEvent(t *testing.T) {
	t.Run("nominal", func(t *testing.T) {
		test := func(userExists bool) func(t *testing.T) {
			return func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
				appsec.TrackUserLoginFailureEvent(ctx, "user id", userExists, map[string]string{"region": "us-east-1"})
				span.Finish()

				// Check the span contains the expected tags.
				require.Len(t, mt.FinishedSpans(), 1)
				finished := mt.FinishedSpans()[0]
				expectedEventPrefix := "appsec.events.users.login.failure."
				require.Equal(t, true, finished.Tag(expectedEventPrefix+"track"))
				require.Equal(t, "user id", finished.Tag(expectedEventPrefix+"usr.id"))
				require.Equal(t, userExists, finished.Tag(expectedEventPrefix+"usr.exists"))
				require.Equal(t, "us-east-1", finished.Tag(expectedEventPrefix+"region"))
			}
		}
		t.Run("user-exists", test(true))
		t.Run("user-not-exists", test(false))
	})

	t.Run("nil-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginFailureEvent(nil, "user id", false, nil)
		})
	})

	t.Run("empty-context", func(t *testing.T) {
		require.NotPanics(t, func() {
			appsec.TrackUserLoginFailureEvent(context.Background(), "user id", false, nil)
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
		require.Equal(t, true, finished.Tag(expectedEventPrefix+"track"))
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
	t.Run("error/appsec-disabled", func(t *testing.T) {
		err := appsec.SetUser(nil, "usr.id")
		require.NotNil(t, err)
		require.False(t, err.ShouldBlock())
		require.Equal(t, "AppSec is not enabled", err.Error())
	})

	privateAppsec.Start()
	defer privateAppsec.Stop()

	t.Run("error/nil-ctx", func(t *testing.T) {
		err := appsec.SetUser(nil, "usr.id")
		require.NotNil(t, err)
		require.False(t, err.ShouldBlock())
		require.Equal(t, "Could not retrieve span from context", err.Error())
	})

	t.Run("no-error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
		defer span.Finish()
		err := appsec.SetUser(ctx, "usr.id")
		require.Nil(t, err)
	})
}

func ExampleTrackUserLoginSuccessEvent() {
	// Create an example span and set a user login success appsec event example to it.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
	defer span.Finish()
	appsec.TrackUserLoginSuccessEvent(ctx, "user id", map[string]string{"region": "us-east-1"}, tracer.WithUserName("username"))
}

func ExampleTrackUserLoginFailureEvent() {
	// Create an example span and set a user login failure appsec event example to it.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "example")
	defer span.Finish()
	appsec.TrackUserLoginFailureEvent(ctx, "user id", false, nil)
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
