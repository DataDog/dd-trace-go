// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
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

func TestClientFlush(t *testing.T) {
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
				require.IsType(t, transport.AppHeartbeat{}, payload)
				assert.Equal(t, payload.RequestType(), transport.RequestTypeAppHeartbeat)
			},
		},
		{
			name: "extended-heartbeat-config",
			clientConfig: ClientConfig{
				HeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.AddAppConfig("key", "value", types.OriginDefault)
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppExtendedHeartbeat{}, payload)
				heartbeat := payload.(transport.AppExtendedHeartbeat)
				assert.Len(t, heartbeat.Configuration, 1)
				assert.Equal(t, heartbeat.Configuration[0].Name, "key")
				assert.Equal(t, heartbeat.Configuration[0].Value, "value")
				assert.Equal(t, heartbeat.Configuration[0].Origin, types.OriginDefault)
			},
		},
		{
			name: "extended-heartbeat-integrations",
			clientConfig: ClientConfig{
				HeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppExtendedHeartbeat{}, payload)
				heartbeat := payload.(transport.AppExtendedHeartbeat)
				assert.Len(t, heartbeat.Integrations, 1)
				assert.Equal(t, heartbeat.Integrations[0].Name, "test-integration")
				assert.Equal(t, heartbeat.Integrations[0].Version, "1.0.0")
			},
		},
		{
			name: "configuration-default",
			when: func(c *client) {
				c.AddAppConfig("key", "value", types.OriginDefault)
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppClientConfigurationChange{}, payload)
				config := payload.(transport.AppClientConfigurationChange)
				assert.Len(t, config.Configuration, 1)
				assert.Equal(t, config.Configuration[0].Name, "key")
				assert.Equal(t, config.Configuration[0].Value, "value")
				assert.Equal(t, config.Configuration[0].Origin, types.OriginDefault)
			},
		},
		{
			name: "configuration-default",
			when: func(c *client) {
				c.AddAppConfig("key", "value", types.OriginDefault)
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppClientConfigurationChange{}, payload)
				config := payload.(transport.AppClientConfigurationChange)
				assert.Len(t, config.Configuration, 1)
				assert.Equal(t, config.Configuration[0].Name, "key")
				assert.Equal(t, config.Configuration[0].Value, "value")
				assert.Equal(t, config.Configuration[0].Origin, types.OriginDefault)
			},
		},
		{
			name: "product-start",
			when: func(c *client) {
				c.ProductStarted("test-product")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppProductChange{}, payload)
				productChange := payload.(transport.AppProductChange)
				assert.Len(t, productChange.Products, 1)
				assert.True(t, productChange.Products[types.Namespace("test-product")].Enabled)
			},
		},
		{
			name: "product-start-error",
			when: func(c *client) {
				c.ProductStartError("test-product", errors.New("test-error"))
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppProductChange{}, payload)
				productChange := payload.(transport.AppProductChange)
				assert.Len(t, productChange.Products, 1)
				assert.False(t, productChange.Products[types.Namespace("test-product")].Enabled)
				assert.Equal(t, "test-error", productChange.Products[types.Namespace("test-product")].Error.Message)
			},
		},
		{
			name: "product-stop",
			when: func(c *client) {
				c.ProductStopped("test-product")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppProductChange{}, payload)
				productChange := payload.(transport.AppProductChange)
				assert.Len(t, productChange.Products, 1)
				assert.False(t, productChange.Products[types.Namespace("test-product")].Enabled)
			},
		},
		{
			name: "integration",
			when: func(c *client) {
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppIntegrationChange{}, payload)
				integrationChange := payload.(transport.AppIntegrationChange)
				assert.Len(t, integrationChange.Integrations, 1)
				assert.Equal(t, integrationChange.Integrations[0].Name, "test-integration")
				assert.Equal(t, integrationChange.Integrations[0].Version, "1.0.0")
				assert.True(t, integrationChange.Integrations[0].Enabled)
			},
		},
		{
			name: "integration-error",
			when: func(c *client) {
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0", Error: "test-error"})
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppIntegrationChange{}, payload)
				integrationChange := payload.(transport.AppIntegrationChange)
				assert.Len(t, integrationChange.Integrations, 1)
				assert.Equal(t, integrationChange.Integrations[0].Name, "test-integration")
				assert.Equal(t, integrationChange.Integrations[0].Version, "1.0.0")
				assert.False(t, integrationChange.Integrations[0].Enabled)
				assert.Equal(t, integrationChange.Integrations[0].Error, "test-error")
			},
		},
		{
			name: "product+integration",
			when: func(c *client) {
				c.ProductStarted("test-product")
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				for _, payload := range batch.Payload {
					switch p := payload.Payload.(type) {
					case transport.AppProductChange:
						assert.Equal(t, transport.RequestTypeAppProductChange, payload.RequestType)
						assert.Len(t, p.Products, 1)
						assert.True(t, p.Products[types.Namespace("test-product")].Enabled)
					case transport.AppIntegrationChange:
						assert.Equal(t, transport.RequestTypeAppIntegrationsChange, payload.RequestType)
						assert.Len(t, p.Integrations, 1)
						assert.Equal(t, p.Integrations[0].Name, "test-integration")
						assert.Equal(t, p.Integrations[0].Version, "1.0.0")
						assert.True(t, p.Integrations[0].Enabled)
					default:
						t.Fatalf("unexpected payload type: %T", p)
					}
				}
			},
		},
		{
			name: "app-started",
			when: func(c *client) {
				c.appStart()
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppStarted{}, payload)
				appStart := payload.(transport.AppStarted)
				assert.Equal(t, appStart.InstallSignature.InstallID, globalconfig.InstrumentationInstallID())
				assert.Equal(t, appStart.InstallSignature.InstallType, globalconfig.InstrumentationInstallType())
				assert.Equal(t, appStart.InstallSignature.InstallTime, globalconfig.InstrumentationInstallTime())
			},
		},
		{
			name: "app-started-with-product",
			when: func(c *client) {
				c.appStart()
				c.ProductStarted("test-product")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppStarted{}, payload)
				appStart := payload.(transport.AppStarted)
				assert.Equal(t, appStart.Products[types.Namespace("test-product")].Enabled, true)
			},
		},
		{
			name: "app-started-with-configuration",
			when: func(c *client) {
				c.appStart()
				c.AddAppConfig("key", "value", types.OriginDefault)
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppStarted{}, payload)
				appStart := payload.(transport.AppStarted)
				require.Len(t, appStart.Configuration, 1)
				assert.Equal(t, appStart.Configuration[0].Name, "key")
				assert.Equal(t, appStart.Configuration[0].Value, "value")
			},
		},
		{
			name: "app-started+integrations",
			when: func(c *client) {
				c.appStart()
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)

				// Check AppStarted is the first payload in MessageBatch
				assert.IsType(t, transport.AppStarted{}, batch.Payload[0].Payload)

				for _, payload := range batch.Payload {
					switch p := payload.Payload.(type) {
					case transport.AppStarted:
						assert.Equal(t, transport.RequestTypeAppStarted, payload.RequestType)
					case transport.AppIntegrationChange:
						assert.Equal(t, transport.RequestTypeAppIntegrationsChange, payload.RequestType)
						assert.Len(t, p.Integrations, 1)
						assert.Equal(t, p.Integrations[0].Name, "test-integration")
						assert.Equal(t, p.Integrations[0].Version, "1.0.0")
					default:
						t.Fatalf("unexpected payload type: %T", p)
					}
				}
			},
		},
		{
			name: "app-started+heartbeat",
			clientConfig: ClientConfig{
				HeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.appStart()
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)

				// Check AppStarted is the first payload in MessageBatch
				assert.IsType(t, transport.AppStarted{}, batch.Payload[0].Payload)

				for _, payload := range batch.Payload {
					switch p := payload.Payload.(type) {
					case transport.AppStarted:
						assert.Equal(t, transport.RequestTypeAppStarted, payload.RequestType)
					case transport.AppHeartbeat:
						assert.Equal(t, transport.RequestTypeAppHeartbeat, payload.RequestType)
					default:
						t.Fatalf("unexpected payload type: %T", p)
					}
				}
			},
		},
		{
			name: "app-stopped",
			when: func(c *client) {
				c.appStop()
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppClosing{}, payload)
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
