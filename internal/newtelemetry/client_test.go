// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

func TestNewClient(t *testing.T) {
	for _, test := range []struct {
		name         string
		tracerConfig internal.TracerConfig
		clientConfig ClientConfig
		newErr       string
	}{
		{
			name: "empty service",
			tracerConfig: internal.TracerConfig{
				Service: "",
				Env:     "test-env",
				Version: "1.0.0",
			},
			newErr: "service name must not be empty",
		},
		{
			name: "empty environment",
			tracerConfig: internal.TracerConfig{
				Service: "test-service",
				Env:     "",
				Version: "1.0.0",
			},
			newErr: "environment name must not be empty",
		},
		{
			name: "empty version",
			tracerConfig: internal.TracerConfig{
				Service: "test-service",
				Env:     "test-env",
				Version: "",
			},
			newErr: "version must not be empty",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, err := NewClient(test.tracerConfig.Service, test.tracerConfig.Env, test.tracerConfig.Version, test.clientConfig)
			if err == nil {
				defer c.Close()
			}

			if test.newErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.newErr)
			}
		})
	}
}

type testWriter struct {
	flush func(transport.Payload)
}

func (w *testWriter) Flush(payloads transport.Payload) (int, error) {
	w.flush(payloads)
	return 1, nil
}

func TestClient(t *testing.T) {
	tracerConfig := internal.TracerConfig{
		Service: "test-service",
		Env:     "test-env",
		Version: "1.0.0",
	}
	for _, test := range []struct {
		name         string
		clientConfig ClientConfig
		when         func(c *client)
		expect       func(*testing.T, transport.Payload)
	}{
		{
			name: "heartbeat",
			clientConfig: ClientConfig{
				HeartbeatInterval: time.Nanosecond,
			},
			expect: func(t *testing.T, payload transport.Payload) {
				assert.Equal(t, payload.RequestType(), transport.RequestTypeAppHeartbeat)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.clientConfig.AgentURL = "http://localhost:8126"
			c, err := newClient(tracerConfig, test.clientConfig)
			require.NoError(t, err)
			defer c.Close()

			var writerFlushCalled bool

			c.writer = &testWriter{
				flush: func(payload transport.Payload) {
					writerFlushCalled = true
					if test.expect != nil {
						test.expect(t, payload)
					}
				},
			}

			if test.when != nil {
				test.when(c)
			}
			c.Flush()
			require.Truef(t, writerFlushCalled, "expected writer.Flush() to be called")
		})
	}
}
