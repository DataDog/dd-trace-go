// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
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
				HeartbeatInterval:         time.Nanosecond,
				ExtendedHeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.AddAppConfig("key", "value", types.OriginDefault)
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				require.Len(t, batch.Payload, 2)
				assert.Equal(t, transport.RequestTypeAppClientConfigurationChange, batch.Payload[0].RequestType)
				assert.Equal(t, transport.RequestTypeAppExtendedHeartBeat, batch.Payload[1].RequestType)

				assert.Len(t, batch.Payload[1].Payload.(transport.AppExtendedHeartbeat).Configuration, 0)
			},
		},
		{
			name: "extended-heartbeat-integrations",
			clientConfig: ClientConfig{
				HeartbeatInterval:         time.Nanosecond,
				ExtendedHeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				require.Len(t, batch.Payload, 2)
				assert.Equal(t, transport.RequestTypeAppIntegrationsChange, batch.Payload[0].RequestType)
				assert.Equal(t, transport.RequestTypeAppExtendedHeartBeat, batch.Payload[1].RequestType)
				assert.Len(t, batch.Payload[1].Payload.(transport.AppExtendedHeartbeat).Integrations, 1)
				assert.Equal(t, batch.Payload[1].Payload.(transport.AppExtendedHeartbeat).Integrations[0].Name, "test-integration")
				assert.Equal(t, batch.Payload[1].Payload.(transport.AppExtendedHeartbeat).Integrations[0].Version, "1.0.0")
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
				assert.Len(t, batch.Payload, 2)
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
			name: "product+integration+heartbeat",
			clientConfig: ClientConfig{
				HeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.ProductStarted("test-product")
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				assert.Len(t, batch.Payload, 3)
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
					case transport.AppHeartbeat:
						assert.Equal(t, transport.RequestTypeAppHeartbeat, payload.RequestType)
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
				switch p := payload.(type) {
				case transport.AppStarted:
					assert.Equal(t, globalconfig.InstrumentationInstallID(), p.InstallSignature.InstallID)
					assert.Equal(t, globalconfig.InstrumentationInstallType(), p.InstallSignature.InstallType)
					assert.Equal(t, globalconfig.InstrumentationInstallTime(), p.InstallSignature.InstallTime)
				case transport.AppIntegrationChange:
					assert.Len(t, p.Integrations, 1)
					assert.Equal(t, p.Integrations[0].Name, "test-integration")
					assert.Equal(t, p.Integrations[0].Version, "1.0.0")
				default:
					t.Fatalf("unexpected payload type: %T", p)
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
				switch p := payload.(type) {
				case transport.AppStarted:
					assert.Equal(t, globalconfig.InstrumentationInstallID(), p.InstallSignature.InstallID)
					assert.Equal(t, globalconfig.InstrumentationInstallType(), p.InstallSignature.InstallType)
					assert.Equal(t, globalconfig.InstrumentationInstallTime(), p.InstallSignature.InstallTime)
				case transport.AppHeartbeat:
				default:
					t.Fatalf("unexpected payload type: %T", p)
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
		{
			name: "app-dependencies-loaded",
			clientConfig: ClientConfig{
				DependencyLoader: func() (*debug.BuildInfo, bool) {
					return &debug.BuildInfo{
						Deps: []*debug.Module{
							{Path: "test", Version: "v1.0.0"},
							{Path: "test2", Version: "v2.0.0"},
							{Path: "test3", Version: "3.0.0"},
						},
					}, true
				},
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppDependenciesLoaded{}, payload)
				deps := payload.(transport.AppDependenciesLoaded)

				assert.Len(t, deps.Dependencies, 3)
				assert.Equal(t, deps.Dependencies[0].Name, "test")
				assert.Equal(t, deps.Dependencies[0].Version, "1.0.0")
				assert.Equal(t, deps.Dependencies[1].Name, "test2")
				assert.Equal(t, deps.Dependencies[1].Version, "2.0.0")
				assert.Equal(t, deps.Dependencies[2].Name, "test3")
				assert.Equal(t, deps.Dependencies[2].Version, "3.0.0")
			},
		},
		{
			name: "app-many-dependencies-loaded",
			clientConfig: ClientConfig{
				DependencyLoader: func() (*debug.BuildInfo, bool) {
					modules := make([]*debug.Module, 2001)
					for i := range modules {
						modules[i] = &debug.Module{
							Path:    fmt.Sprintf("test-%d", i),
							Version: fmt.Sprintf("v%d.0.0", i),
						}
					}
					return &debug.BuildInfo{
						Deps: modules,
					}, true
				},
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.AppDependenciesLoaded{}, payload)
				deps := payload.(transport.AppDependenciesLoaded)

				if len(deps.Dependencies) != 2000 && len(deps.Dependencies) != 1 {
					t.Fatalf("expected 2000 and 1 dependencies, got %d", len(deps.Dependencies))
				}

				if len(deps.Dependencies) == 1 {
					assert.Equal(t, deps.Dependencies[0].Name, "test-0")
					assert.Equal(t, deps.Dependencies[0].Version, "0.0.0")
					return
				}

				for i := range deps.Dependencies {
					assert.Equal(t, deps.Dependencies[i].Name, fmt.Sprintf("test-%d", i))
					assert.Equal(t, deps.Dependencies[i].Version, fmt.Sprintf("%d.0.0", i))
				}
			},
		},
		{
			name: "single-log-debug",
			when: func(c *client) {
				c.Log(LogDebug, "test")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelDebug)
				assert.Equal(t, logs.Logs[0].Message, "test")
			},
		},
		{
			name: "single-log-warn",
			when: func(c *client) {
				c.Log(LogWarn, "test")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelWarn)
				assert.Equal(t, logs.Logs[0].Message, "test")
			},
		},
		{
			name: "single-log-error",
			when: func(c *client) {
				c.Log(LogError, "test")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[0].Message, "test")
			},
		},
		{
			name: "multiple-logs-same-key",
			when: func(c *client) {
				c.Log(LogError, "test")
				c.Log(LogError, "test")
				c.Log(LogError, "test")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[0].Message, "test")
				assert.Equal(t, logs.Logs[0].Count, uint32(3))
			},
		},
		{
			name: "single-log-with-tag",
			when: func(c *client) {
				c.Log(LogError, "test", WithTags(map[string]string{"key": "value"}))
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[0].Message, "test")
				assert.Equal(t, logs.Logs[0].Tags, "key:value")
			},
		},
		{
			name: "single-log-with-tags",
			when: func(c *client) {
				c.Log(LogError, "test", WithTags(map[string]string{"key": "value", "key2": "value2"}))
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[0].Message, "test")
				tags := strings.Split(logs.Logs[0].Tags, ",")
				assert.Contains(t, tags, "key:value")
				assert.Contains(t, tags, "key2:value2")
			},
		},
		{
			name: "single-log-with-tags-and-without",
			when: func(c *client) {
				c.Log(LogError, "test", WithTags(map[string]string{"key": "value", "key2": "value2"}))
				c.Log(LogError, "test")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 2)

				slices.SortStableFunc(logs.Logs, func(i, j transport.LogMessage) int {
					return strings.Compare(i.Tags, j.Tags)
				})

				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[0].Message, "test")
				assert.Equal(t, logs.Logs[0].Count, uint32(1))
				assert.Empty(t, logs.Logs[0].Tags)

				assert.Equal(t, logs.Logs[1].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[1].Message, "test")
				assert.Equal(t, logs.Logs[1].Count, uint32(1))
				tags := strings.Split(logs.Logs[1].Tags, ",")
				assert.Contains(t, tags, "key:value")
				assert.Contains(t, tags, "key2:value2")
			},
		},
		{
			name: "single-log-with-stacktrace",
			when: func(c *client) {
				c.Log(LogError, "test", WithStacktrace())
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[0].Message, "test")
				assert.Contains(t, logs.Logs[0].StackTrace, "internal/newtelemetry/client_test.go")
			},
		},
		{
			name: "single-log-with-stacktrace-and-tags",
			when: func(c *client) {
				c.Log(LogError, "test", WithStacktrace(), WithTags(map[string]string{"key": "value", "key2": "value2"}))
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[0].Message, "test")
				assert.Contains(t, logs.Logs[0].StackTrace, "internal/newtelemetry/client_test.go")
				tags := strings.Split(logs.Logs[0].Tags, ",")
				assert.Contains(t, tags, "key:value")
				assert.Contains(t, tags, "key2:value2")

			},
		},
		{
			name: "multiple-logs-different-levels",
			when: func(c *client) {
				c.Log(LogError, "test")
				c.Log(LogWarn, "test")
				c.Log(LogDebug, "test")
			},
			expect: func(t *testing.T, payload transport.Payload) {
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 3)

				slices.SortStableFunc(logs.Logs, func(i, j transport.LogMessage) int {
					return strings.Compare(string(i.Level), string(j.Level))
				})

				assert.Equal(t, logs.Logs[0].Level, transport.LogLevelDebug)
				assert.Equal(t, logs.Logs[0].Message, "test")
				assert.Equal(t, logs.Logs[0].Count, uint32(1))
				assert.Equal(t, logs.Logs[1].Level, transport.LogLevelError)
				assert.Equal(t, logs.Logs[1].Message, "test")
				assert.Equal(t, logs.Logs[1].Count, uint32(1))
				assert.Equal(t, logs.Logs[2].Level, transport.LogLevelWarn)
				assert.Equal(t, logs.Logs[2].Message, "test")
				assert.Equal(t, logs.Logs[2].Count, uint32(1))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := defaultConfig(test.clientConfig)
			config.AgentURL = "http://localhost:8126"
			config.DependencyLoader = test.clientConfig.DependencyLoader // Don't use the default dependency loader
			c, err := newClient(tracerConfig, config)
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

type testRoundTripper struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.roundTrip(req)
}

func TestClientEnd2End(t *testing.T) {
	tracerConfig := internal.TracerConfig{
		Service: "test-service",
		Env:     "test-env",
		Version: "1.0.0",
	}

	parseRequest := func(t *testing.T, request *http.Request) transport.Body {
		assert.Equal(t, "v2", request.Header.Get("DD-Telemetry-API-Version"))
		assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
		assert.Equal(t, "go", request.Header.Get("DD-Client-Library-Language"))
		assert.Equal(t, "test-env", request.Header.Get("DD-Agent-Env"))
		assert.Equal(t, version.Tag, request.Header.Get("DD-Client-Library-Version"))
		assert.Equal(t, globalconfig.InstrumentationInstallID(), request.Header.Get("DD-Agent-Install-Id"))
		assert.Equal(t, globalconfig.InstrumentationInstallType(), request.Header.Get("DD-Agent-Install-Type"))
		assert.Equal(t, globalconfig.InstrumentationInstallTime(), request.Header.Get("DD-Agent-Install-Time"))
		assert.Equal(t, "true", request.Header.Get("DD-Telemetry-Debug-Enabled"))

		assert.NotEmpty(t, request.Header.Get("DD-Agent-Hostname"))

		var body transport.Body
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))

		assert.Equal(t, string(body.RequestType), request.Header.Get("DD-Telemetry-Request-Type"))
		assert.Equal(t, "test-service", body.Application.ServiceName)
		assert.Equal(t, "test-env", body.Application.Env)
		assert.Equal(t, "1.0.0", body.Application.ServiceVersion)
		assert.Equal(t, "go", body.Application.LanguageName)
		assert.Equal(t, runtime.Version(), body.Application.LanguageVersion)

		assert.NotEmpty(t, body.Host.Hostname)
		assert.Equal(t, osinfo.OSName(), body.Host.OS)
		assert.Equal(t, osinfo.OSVersion(), body.Host.OSVersion)
		assert.Equal(t, osinfo.Architecture(), body.Host.Architecture)
		assert.Equal(t, osinfo.KernelName(), body.Host.KernelName)
		assert.Equal(t, osinfo.KernelRelease(), body.Host.KernelRelease)
		assert.Equal(t, osinfo.KernelVersion(), body.Host.KernelVersion)

		assert.Equal(t, true, body.Debug)
		assert.Equal(t, "v2", body.APIVersion)
		assert.NotZero(t, body.TracerTime)
		assert.LessOrEqual(t, int64(1), body.SeqID)
		assert.Equal(t, globalconfig.RuntimeID(), body.RuntimeID)

		return body
	}

	for _, test := range []struct {
		name   string
		when   func(*client)
		expect func(*testing.T, *http.Request) (*http.Response, error)
	}{
		{
			name: "app-start",
			when: func(c *client) {
				c.appStart()
			},
			expect: func(t *testing.T, request *http.Request) (*http.Response, error) {
				assert.Equal(t, string(transport.RequestTypeAppStarted), request.Header.Get("DD-Telemetry-Request-Type"))
				body := parseRequest(t, request)
				assert.Equal(t, transport.RequestTypeAppStarted, body.RequestType)
				return &http.Response{
					StatusCode: http.StatusOK,
				}, nil
			},
		},
		{
			name: "app-stop",
			when: func(c *client) {
				c.appStop()
			},
			expect: func(t *testing.T, request *http.Request) (*http.Response, error) {
				assert.Equal(t, string(transport.RequestTypeAppClosing), request.Header.Get("DD-Telemetry-Request-Type"))
				body := parseRequest(t, request)
				assert.Equal(t, transport.RequestTypeAppClosing, body.RequestType)
				return &http.Response{
					StatusCode: http.StatusOK,
				}, nil
			},
		},
		{
			name: "app-start+app-stop",
			when: func(c *client) {
				c.appStart()
				c.appStop()
			},
			expect: func(t *testing.T, request *http.Request) (*http.Response, error) {
				body := parseRequest(t, request)

				switch request.Header.Get("DD-Telemetry-Request-Type") {
				case string(transport.RequestTypeAppStarted):
					payload := body.Payload.(*transport.AppStarted)
					assert.Equal(t, globalconfig.InstrumentationInstallID(), payload.InstallSignature.InstallID)
					assert.Equal(t, globalconfig.InstrumentationInstallType(), payload.InstallSignature.InstallType)
					assert.Equal(t, globalconfig.InstrumentationInstallTime(), payload.InstallSignature.InstallTime)
				case string(transport.RequestTypeAppClosing):

				default:
					t.Fatalf("unexpected request type: %s", request.Header.Get("DD-Telemetry-Request-Type"))
				}

				return &http.Response{
					StatusCode: http.StatusOK,
				}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clientConfig := ClientConfig{
				AgentURL: "http://localhost:8126",
				HTTPClient: &http.Client{
					Timeout: 5 * time.Second,
					Transport: &testRoundTripper{
						roundTrip: func(req *http.Request) (*http.Response, error) {
							if test.expect != nil {
								return test.expect(t, req)
							}
							return &http.Response{
								StatusCode: http.StatusOK,
							}, nil
						},
					},
				},
				Debug: true,
			}

			clientConfig = defaultConfig(clientConfig)
			clientConfig.DependencyLoader = nil

			c, err := newClient(tracerConfig, clientConfig)
			require.NoError(t, err)
			defer c.Close()

			test.when(c)
			c.Flush()
		})
	}
}

func BenchmarkLogs(b *testing.B) {
	clientConfig := ClientConfig{
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		AgentURL:                  "http://localhost:8126",
	}

	b.Run("simple", func(b *testing.B) {
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogDebug, "this is supposed to be a DEBUG log of representative length with a variable message: "+strconv.Itoa(i%10))
		}
	})

	b.Run("with-tags", func(b *testing.B) {
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogWarn, "this is supposed to be a WARN log of representative length", WithTags(map[string]string{"key": strconv.Itoa(i % 10)}))
		}
	})

	b.Run("with-stacktrace", func(b *testing.B) {
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogError, "this is supposed to be a ERROR log of representative length", WithStacktrace())
		}
	})
}

func BenchmarkWorstCaseScenarioFloodLogging(b *testing.B) {
	nbSameLogs := 10
	nbDifferentLogs := 100
	nbGoroutines := 25

	clientConfig := ClientConfig{
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		FlushIntervalRange: struct {
			Min time.Duration
			Max time.Duration
		}{Min: time.Second, Max: time.Second},
		AgentURL: "http://localhost:8126",

		// Empty transport to avoid sending data to the agent
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &testRoundTripper{
				roundTrip: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
					}, nil
				},
			},
		},
	}

	c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
	require.NoError(b, err)

	defer c.Close()

	b.ResetTimer()

	for x := 0; x < b.N; x++ {
		var wg sync.WaitGroup

		for i := 0; i < nbGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < nbDifferentLogs; j++ {
					for k := 0; k < nbSameLogs; k++ {
						c.Log(LogDebug, "this is supposed to be a DEBUG log of representative length"+strconv.Itoa(i), WithTags(map[string]string{"key": strconv.Itoa(j)}))
					}
				}
			}()
		}

		wg.Wait()
	}

	b.Log("Called (*client).Log ", nbGoroutines*nbDifferentLogs*nbSameLogs, " times")
}
