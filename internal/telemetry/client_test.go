// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/osinfo"
	"github.com/DataDog/dd-trace-go/v2/internal/synctest"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func TestNewClient(t *testing.T) {
	for _, test := range []struct {
		name         string
		tracerConfig internal.TracerConfig
		clientConfig ClientConfig
		newErr       string
	}{
		{
			name: "nominal",
			tracerConfig: internal.TracerConfig{
				Service: "test-service",
				Env:     "test-env",
				Version: "1.0.0",
			},
			clientConfig: ClientConfig{
				AgentURL: "http://localhost:8126",
			},
		},
		{
			name:         "empty service",
			tracerConfig: internal.TracerConfig{},
			newErr:       "service name must not be empty",
		},
		{
			name: "empty agent url",
			tracerConfig: internal.TracerConfig{
				Service: "test-service",
			},
			clientConfig: ClientConfig{},
			newErr:       "could not build any endpoint",
		},
		{
			name: "invalid agent url",
			tracerConfig: internal.TracerConfig{
				Service: "test-service",
			},
			clientConfig: ClientConfig{
				AgentURL: "toto_protocol://localhost:8126",
			},
			newErr: "invalid agent URL",
		},
		{
			name: "Too big payload size",
			tracerConfig: internal.TracerConfig{
				Service: "test-service",
			},
			clientConfig: ClientConfig{
				AgentURL:              "http://localhost:8126",
				EarlyFlushPayloadSize: 64 * 1024 * 1024, // 64MB
			},
			newErr: "EarlyFlushPayloadSize must be between 0 and 5MB",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DD_API_KEY", "") // In case one is present in the environment...

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

func TestAutoFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := defaultConfig(ClientConfig{
			AgentURL: "http://localhost:8126",
		})
		c, err := newClient(internal.TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		}, config)
		require.NoError(t, err)
		defer c.Close()

		recordWriter := &internal.RecordWriter{}
		c.writer = recordWriter

		time.Sleep(config.FlushInterval.Max + time.Second)

		require.Len(t, recordWriter.Payloads(), 1)
	})
}

func TestClientFlush(t *testing.T) {
	tracerConfig := internal.TracerConfig{
		Service: "test-service",
		Env:     "test-env",
		Version: "1.0.0",
	}

	type testParams struct {
		name         string
		clientConfig ClientConfig
		when         func(c *client)
		expect       func(*testing.T, []transport.Payload)
	}

	testcases := []testParams{
		{
			name: "heartbeat",
			clientConfig: ClientConfig{
				HeartbeatInterval: time.Nanosecond,
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppHeartbeat{}, payload)
				assert.Equal(t, payload.RequestType(), transport.RequestTypeAppHeartbeat)
			},
		},
		{
			name: "extended-heartbeat-config",
			clientConfig: ClientConfig{
				ExtendedHeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.RegisterAppConfig("key", "value", OriginDefault)

				// Make sure the limiter of the heartbeat is triggered
				time.Sleep(time.Microsecond)
				runtime.Gosched()
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				require.Len(t, batch, 2)
				assert.Equal(t, transport.RequestTypeAppClientConfigurationChange, batch[0].RequestType)
				assert.Equal(t, transport.RequestTypeAppExtendedHeartBeat, batch[1].RequestType)

				assert.Len(t, batch[1].Payload.(transport.AppExtendedHeartbeat).Configuration, 0)
			},
		},
		{
			name: "extended-heartbeat-integrations",
			clientConfig: ClientConfig{
				ExtendedHeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})

				// Make sure the limiter of the heartbeat is triggered
				time.Sleep(time.Microsecond)
				runtime.Gosched()
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				require.Len(t, batch, 2)
				assert.Equal(t, transport.RequestTypeAppIntegrationsChange, batch[0].RequestType)
				assert.Equal(t, transport.RequestTypeAppExtendedHeartBeat, batch[1].RequestType)
				assert.Len(t, batch[1].Payload.(transport.AppExtendedHeartbeat).Integrations, 1)
				assert.Equal(t, batch[1].Payload.(transport.AppExtendedHeartbeat).Integrations[0].Name, "test-integration")
				assert.Equal(t, batch[1].Payload.(transport.AppExtendedHeartbeat).Integrations[0].Version, "1.0.0")
			},
		},
		{
			name: "configuration-default",
			when: func(c *client) {
				c.RegisterAppConfig("key", "value", OriginDefault)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppClientConfigurationChange{}, payload)
				config := payload.(transport.AppClientConfigurationChange)
				assert.Len(t, config.Configuration, 1)
				assert.Equal(t, config.Configuration[0].Name, "key")
				assert.Equal(t, config.Configuration[0].Value, "value")
				assert.Equal(t, config.Configuration[0].Origin, OriginDefault)
			},
		},
		{
			name: "configuration-complex-values",
			when: func(c *client) {
				c.RegisterAppConfigs(
					Configuration{Name: "key1", Value: []string{"value1", "value2"}, Origin: OriginDefault},
					Configuration{Name: "key2", Value: map[string]string{"key": "value", "key2": "value2"}, Origin: OriginCode},
					Configuration{Name: "key3", Value: []int{1, 2, 3}, Origin: OriginDDConfig},
					Configuration{Name: "key4", Value: struct {
						A string
					}{A: "1"}, Origin: OriginEnvVar},
					Configuration{Name: "key5", Value: map[int]struct{ X int }{1: {X: 1}}, Origin: OriginRemoteConfig},
				)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppClientConfigurationChange{}, payload)
				config := payload.(transport.AppClientConfigurationChange)

				slices.SortStableFunc(config.Configuration, func(a, b transport.ConfKeyValue) int {
					return strings.Compare(a.Name, b.Name)
				})

				assert.Len(t, config.Configuration, 5)
				assert.Equal(t, "key1", config.Configuration[0].Name)
				assert.Equal(t, "value1,value2", config.Configuration[0].Value)
				assert.Equal(t, OriginDefault, config.Configuration[0].Origin)
				assert.Equal(t, "key2", config.Configuration[1].Name)
				assert.Equal(t, "key:value,key2:value2", config.Configuration[1].Value)
				assert.Equal(t, OriginCode, config.Configuration[1].Origin)
				assert.Equal(t, "key3", config.Configuration[2].Name)
				assert.Equal(t, "[1 2 3]", config.Configuration[2].Value)
				assert.Equal(t, OriginDDConfig, config.Configuration[2].Origin)
				assert.Equal(t, "key4", config.Configuration[3].Name)
				assert.Equal(t, "{1}", config.Configuration[3].Value)
				assert.Equal(t, OriginEnvVar, config.Configuration[3].Origin)
				assert.Equal(t, "key5", config.Configuration[4].Name)
				assert.Equal(t, "1:{1}", config.Configuration[4].Value)
				assert.Equal(t, OriginRemoteConfig, config.Configuration[4].Origin)
			},
		},
		{
			name: "product-start",
			when: func(c *client) {
				c.ProductStarted("test-product")
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppProductChange{}, payload)
				productChange := payload.(transport.AppProductChange)
				assert.Len(t, productChange.Products, 1)
				assert.True(t, productChange.Products[Namespace("test-product")].Enabled)
			},
		},
		{
			name: "product-start-error",
			when: func(c *client) {
				c.ProductStartError("test-product", errors.New("test-error"))
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppProductChange{}, payload)
				productChange := payload.(transport.AppProductChange)
				assert.Len(t, productChange.Products, 1)
				assert.False(t, productChange.Products[Namespace("test-product")].Enabled)
				assert.Equal(t, "test-error", productChange.Products[Namespace("test-product")].Error.Message)
			},
		},
		{
			name: "product-stop",
			when: func(c *client) {
				c.ProductStopped("test-product")
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppProductChange{}, payload)
				productChange := payload.(transport.AppProductChange)
				assert.Len(t, productChange.Products, 1)
				assert.False(t, productChange.Products[Namespace("test-product")].Enabled)
			},
		},
		{
			name: "integration",
			when: func(c *client) {
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				assert.Len(t, batch, 2)
				for _, payload := range batch {
					switch p := payload.Payload.(type) {
					case transport.AppProductChange:
						assert.Equal(t, transport.RequestTypeAppProductChange, payload.RequestType)
						assert.Len(t, p.Products, 1)
						assert.True(t, p.Products[Namespace("test-product")].Enabled)
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

				// Make sure the limiter of the heartbeat is triggered
				time.Sleep(time.Microsecond)
				runtime.Gosched()
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				assert.Len(t, batch, 3)
				for _, payload := range batch {
					switch p := payload.Payload.(type) {
					case transport.AppProductChange:
						assert.Equal(t, transport.RequestTypeAppProductChange, payload.RequestType)
						assert.Len(t, p.Products, 1)
						assert.True(t, p.Products[Namespace("test-product")].Enabled)
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
				c.AppStart()
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
				c.AppStart()
				c.ProductStarted("test-product")
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppStarted{}, payload)
				appStart := payload.(transport.AppStarted)
				assert.Equal(t, appStart.Products[Namespace("test-product")].Enabled, true)
			},
		},
		{
			name: "app-started-with-configuration",
			when: func(c *client) {
				c.AppStart()
				c.RegisterAppConfig("key", "value", OriginDefault)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
				c.AppStart()
				c.MarkIntegrationAsLoaded(Integration{Name: "test-integration", Version: "1.0.0"})
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppStarted{}, payload)
				appStart := payload.(transport.AppStarted)
				assert.Equal(t, globalconfig.InstrumentationInstallID(), appStart.InstallSignature.InstallID)
				assert.Equal(t, globalconfig.InstrumentationInstallType(), appStart.InstallSignature.InstallType)
				assert.Equal(t, globalconfig.InstrumentationInstallTime(), appStart.InstallSignature.InstallTime)

				payload = payloads[1]
				require.IsType(t, transport.AppIntegrationChange{}, payload)
				p := payload.(transport.AppIntegrationChange)

				assert.Len(t, p.Integrations, 1)
				assert.Equal(t, p.Integrations[0].Name, "test-integration")
				assert.Equal(t, p.Integrations[0].Version, "1.0.0")
			},
		},
		{
			name: "app-started+heartbeat",
			clientConfig: ClientConfig{
				HeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.AppStart()

				// Make sure the limiter of the heartbeat is triggered
				time.Sleep(time.Microsecond)
				runtime.Gosched()
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.AppStarted{}, payload)
				appStart := payload.(transport.AppStarted)
				assert.Equal(t, globalconfig.InstrumentationInstallID(), appStart.InstallSignature.InstallID)
				assert.Equal(t, globalconfig.InstrumentationInstallType(), appStart.InstallSignature.InstallType)
				assert.Equal(t, globalconfig.InstrumentationInstallTime(), appStart.InstallSignature.InstallTime)

				payload = payloads[1]
				require.IsType(t, transport.AppHeartbeat{}, payload)
			},
		},
		{
			name: "app-stopped",
			when: func(c *client) {
				c.AppStop()
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelDebug, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
			},
		},
		{
			name: "single-log-warn",
			when: func(c *client) {
				c.Log(LogWarn, "test")
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelWarn, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
			},
		},
		{
			name: "single-log-error",
			when: func(c *client) {
				c.Log(LogError, "test")
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
			},
		},
		{
			name: "multiple-logs-same-key",
			when: func(c *client) {
				c.Log(LogError, "test")
				c.Log(LogError, "test")
				c.Log(LogError, "test")
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
				assert.Equal(t, uint32(3), logs.Logs[0].Count)
			},
		},
		{
			name: "single-log-with-tag",
			when: func(c *client) {
				c.Log(LogError, "test", WithTags([]string{"key:value"}))
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
				assert.Equal(t, "key:value", logs.Logs[0].Tags)
			},
		},
		{
			name: "single-log-with-tags",
			when: func(c *client) {
				c.Log(LogError, "test", WithTags([]string{"key:value", "key2:value2"}))
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
				tags := strings.Split(logs.Logs[0].Tags, ",")
				assert.Contains(t, tags, "key:value")
				assert.Contains(t, tags, "key2:value2")
			},
		},
		{
			name: "single-log-with-tags-and-without",
			when: func(c *client) {
				c.Log(LogError, "test", WithTags([]string{"key:value", "key2:value2"}))
				c.Log(LogError, "test")
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 2)

				slices.SortStableFunc(logs.Logs, func(i, j transport.LogMessage) int {
					return strings.Compare(i.Tags, j.Tags)
				})

				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
				assert.Equal(t, uint32(1), logs.Logs[0].Count)
				assert.Empty(t, logs.Logs[0].Tags)

				assert.Equal(t, transport.LogLevelError, logs.Logs[1].Level)
				assert.Equal(t, "test", logs.Logs[1].Message)
				assert.Equal(t, uint32(1), logs.Logs[1].Count)
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
				assert.Contains(t, logs.Logs[0].StackTrace, "internal/telemetry/client_test.go")
			},
		},
		{
			name: "single-log-with-stacktrace-and-tags",
			when: func(c *client) {
				c.Log(LogError, "test", WithStacktrace(), WithTags([]string{"key:value", "key2:value2"}))
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
				assert.Contains(t, logs.Logs[0].StackTrace, "internal/telemetry/client_test.go")
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 3)

				slices.SortStableFunc(logs.Logs, func(i, j transport.LogMessage) int {
					return strings.Compare(string(i.Level), string(j.Level))
				})

				assert.Equal(t, transport.LogLevelDebug, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
				assert.Equal(t, uint32(1), logs.Logs[0].Count)
				assert.Equal(t, transport.LogLevelError, logs.Logs[1].Level)
				assert.Equal(t, "test", logs.Logs[1].Message)
				assert.Equal(t, uint32(1), logs.Logs[1].Count)
				assert.Equal(t, transport.LogLevelWarn, logs.Logs[2].Level)
				assert.Equal(t, "test", logs.Logs[2].Message)
				assert.Equal(t, uint32(1), logs.Logs[2].Count)
			},
		},
		{
			name: "simple-count",
			when: func(c *client) {
				c.Count(NamespaceTracers, "init_time", nil).Submit(1)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 1)
				assert.Equal(t, transport.CountMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time", metrics.Series[0].Metric)
				assert.Empty(t, metrics.Series[0].Tags)
				assert.NotZero(t, metrics.Series[0].Points[0][0])
				assert.Equal(t, 1.0, metrics.Series[0].Points[0][1])
			},
		},
		{
			name: "count-multiple-call-same-handle",
			when: func(c *client) {
				handle1 := c.Count(NamespaceTracers, "init_time", nil)
				handle2 := c.Count(NamespaceTracers, "init_time", nil)

				handle2.Submit(1)
				handle1.Submit(1)
				handle1.Submit(3)
				handle2.Submit(2)
				handle2.Submit(10)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 1)
				assert.Equal(t, transport.CountMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time", metrics.Series[0].Metric)
				assert.Empty(t, metrics.Series[0].Tags)
				assert.NotZero(t, metrics.Series[0].Points[0][0])
				assert.Equal(t, 17.0, metrics.Series[0].Points[0][1])
			},
		},
		{
			name: "multiple-count-by-name",
			when: func(c *client) {
				c.Count(NamespaceTracers, "init_time_1", nil).Submit(1)
				c.Count(NamespaceTracers, "init_time_2", nil).Submit(2)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 2)

				assert.Equal(t, transport.CountMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time_"+strconv.Itoa(int(metrics.Series[0].Points[0][1].(float64))), metrics.Series[0].Metric)

				assert.Empty(t, metrics.Series[0].Tags)
				assert.NotZero(t, metrics.Series[0].Points[0][0])

				assert.Equal(t, transport.CountMetric, metrics.Series[1].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[1].Namespace)
				assert.Equal(t, "init_time_"+strconv.Itoa(int(metrics.Series[1].Points[0][1].(float64))), metrics.Series[1].Metric)
				assert.Empty(t, metrics.Series[1].Tags)
				assert.NotZero(t, metrics.Series[1].Points[0][0])
			},
		},
		{
			name: "multiple-count-by-tags",
			when: func(c *client) {
				c.Count(NamespaceTracers, "init_time", []string{"test:1"}).Submit(1)
				c.Count(NamespaceTracers, "init_time", []string{"test:2"}).Submit(2)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 2)

				assert.Equal(t, transport.CountMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time", metrics.Series[0].Metric)

				assert.Contains(t, metrics.Series[0].Tags, "test:"+strconv.Itoa(int(metrics.Series[0].Points[0][1].(float64))))
				assert.NotZero(t, metrics.Series[0].Points[0][0])

				assert.Equal(t, transport.CountMetric, metrics.Series[1].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[1].Namespace)
				assert.Equal(t, "init_time", metrics.Series[1].Metric)
				assert.Contains(t, metrics.Series[1].Tags, "test:"+strconv.Itoa(int(metrics.Series[1].Points[0][1].(float64))))
				assert.NotZero(t, metrics.Series[1].Points[0][0])
			},
		},
		{
			name: "simple-gauge",
			when: func(c *client) {
				c.Gauge(NamespaceTracers, "init_time", nil).Submit(1)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 1)
				assert.Equal(t, transport.GaugeMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time", metrics.Series[0].Metric)
				assert.Empty(t, metrics.Series[0].Tags)
				assert.NotZero(t, metrics.Series[0].Points[0][0])
				assert.Equal(t, 1.0, metrics.Series[0].Points[0][1])
			},
		},
		{
			name: "multiple-gauge-by-name",
			when: func(c *client) {
				c.Gauge(NamespaceTracers, "init_time_1", nil).Submit(1)
				c.Gauge(NamespaceTracers, "init_time_2", nil).Submit(2)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 2)

				assert.Equal(t, transport.GaugeMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time_"+strconv.Itoa(int(metrics.Series[0].Points[0][1].(float64))), metrics.Series[0].Metric)

				assert.Empty(t, metrics.Series[0].Tags)
				assert.NotZero(t, metrics.Series[0].Points[0][0])

				assert.Equal(t, transport.GaugeMetric, metrics.Series[1].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[1].Namespace)
				assert.Equal(t, "init_time_"+strconv.Itoa(int(metrics.Series[1].Points[0][1].(float64))), metrics.Series[1].Metric)
				assert.Empty(t, metrics.Series[1].Tags)
				assert.NotZero(t, metrics.Series[1].Points[0][0])
			},
		},
		{
			name: "multiple-gauge-by-tags",
			when: func(c *client) {
				c.Gauge(NamespaceTracers, "init_time", []string{"test:1"}).Submit(1)
				c.Gauge(NamespaceTracers, "init_time", []string{"test:2"}).Submit(2)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 2)

				assert.Equal(t, transport.GaugeMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time", metrics.Series[0].Metric)

				assert.Contains(t, metrics.Series[0].Tags, "test:"+strconv.Itoa(int(metrics.Series[0].Points[0][1].(float64))))
				assert.NotZero(t, metrics.Series[0].Points[0][0])

				assert.Equal(t, transport.GaugeMetric, metrics.Series[1].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[1].Namespace)
				assert.Equal(t, "init_time", metrics.Series[1].Metric)
				assert.Contains(t, metrics.Series[1].Tags, "test:"+strconv.Itoa(int(metrics.Series[1].Points[0][1].(float64))))
				assert.NotZero(t, metrics.Series[1].Points[0][0])
			},
		},
		{
			name: "simple-rate",
			when: func(c *client) {
				handle := c.Rate(NamespaceTracers, "init_time", nil)
				handle.Submit(1)

				rate := handle.(*rate)
				// So the rate is not +Infinity because the interval is zero
				now := rate.intervalStart.Load()
				sub := now.Add(-time.Second)
				rate.intervalStart.Store(&sub)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.GenerateMetrics{}, payload)
				metrics := payload.(transport.GenerateMetrics)
				require.Len(t, metrics.Series, 1)
				assert.Equal(t, transport.RateMetric, metrics.Series[0].Type)
				assert.Equal(t, NamespaceTracers, metrics.Series[0].Namespace)
				assert.Equal(t, "init_time", metrics.Series[0].Metric)
				assert.Empty(t, metrics.Series[0].Tags)
				assert.NotZero(t, metrics.Series[0].Interval)
				assert.NotZero(t, metrics.Series[0].Points[0][0])
				assert.LessOrEqual(t, metrics.Series[0].Points[0][1], 1.1)
			},
		},
		{
			name: "simple-distribution",
			when: func(c *client) {
				c.Distribution(NamespaceGeneral, "init_time", nil).Submit(1)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, payload, transport.Distributions{})
				distributions := payload.(transport.Distributions)
				require.Len(t, distributions.Series, 1)
				assert.Equal(t, NamespaceGeneral, distributions.Series[0].Namespace)
				assert.Equal(t, "init_time", distributions.Series[0].Metric)
				assert.Empty(t, distributions.Series[0].Tags)
				require.Len(t, distributions.Series[0].Points, 1)
				assert.Equal(t, 1.0, distributions.Series[0].Points[0])
			},
		},
		{
			name: "multiple-distribution-by-name",
			when: func(c *client) {
				c.Distribution(NamespaceTracers, "init_time_1", nil).Submit(1)
				c.Distribution(NamespaceTracers, "init_time_2", nil).Submit(2)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, payload, transport.Distributions{})
				distributions := payload.(transport.Distributions)
				require.Len(t, distributions.Series, 2)

				assert.Equal(t, "init_time_"+strconv.Itoa(int(distributions.Series[0].Points[0])), distributions.Series[0].Metric)
				assert.Equal(t, "init_time_"+strconv.Itoa(int(distributions.Series[1].Points[0])), distributions.Series[1].Metric)
			},
		},
		{
			name: "multiple-distribution-by-tags",
			when: func(c *client) {
				c.Distribution(NamespaceTracers, "init_time", []string{"test:1"}).Submit(1)
				c.Distribution(NamespaceTracers, "init_time", []string{"test:2"}).Submit(2)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, payload, transport.Distributions{})
				distributions := payload.(transport.Distributions)
				require.Len(t, distributions.Series, 2)

				assert.Contains(t, distributions.Series[0].Tags, "test:"+strconv.Itoa(int(distributions.Series[0].Points[0])))
				assert.Contains(t, distributions.Series[1].Tags, "test:"+strconv.Itoa(int(distributions.Series[1].Points[0])))
			},
		},
		{
			name: "distribution-overflow",
			when: func(c *client) {
				handler := c.Distribution(NamespaceGeneral, "init_time", nil)
				for i := 0; i < 1<<16; i++ {
					handler.Submit(float64(i))
				}
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, payload, transport.Distributions{})
				distributions := payload.(transport.Distributions)
				require.Len(t, distributions.Series, 1)
				assert.Equal(t, NamespaceGeneral, distributions.Series[0].Namespace)
				assert.Equal(t, "init_time", distributions.Series[0].Metric)
				assert.Empty(t, distributions.Series[0].Tags)

				// Should not contain the first passed point
				assert.NotContains(t, distributions.Series[0].Points, 0.0)
			},
		},
	}

	for _, test := range testcases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := defaultConfig(test.clientConfig)
			config.AgentURL = "http://localhost:8126"
			config.DependencyLoader = test.clientConfig.DependencyLoader             // Don't use the default dependency loader
			config.internalMetricsEnabled = test.clientConfig.internalMetricsEnabled // only enabled internal metrics when explicitly set
			config.internalMetricsEnabled = false
			config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
			c, err := newClient(tracerConfig, config)
			require.NoError(t, err)
			t.Cleanup(func() {
				c.Close()
			})

			recordWriter := &internal.RecordWriter{}
			c.writer = recordWriter

			if test.when != nil {
				test.when(c)
			}
			c.Flush()

			payloads := recordWriter.Payloads()
			require.LessOrEqual(t, 1, len(payloads))
			test.expect(t, payloads)
		})
	}
}

func TestMetricsDisabled(t *testing.T) {
	t.Setenv("DD_TELEMETRY_METRICS_ENABLED", "false")

	c, err := NewClient("test-service", "test-env", "1.0.0", ClientConfig{AgentURL: "http://localhost:8126"})
	require.NoError(t, err)

	recordWriter := &internal.RecordWriter{}
	c.(*client).writer = recordWriter

	defer c.Close()

	assert.NotNil(t, c.Gauge(NamespaceTracers, "init_time", nil))
	assert.NotNil(t, c.Count(NamespaceTracers, "init_time", nil))
	assert.NotNil(t, c.Rate(NamespaceTracers, "init_time", nil))
	assert.NotNil(t, c.Distribution(NamespaceGeneral, "init_time", nil))

	c.Flush()

	payloads := recordWriter.Payloads()
	require.Len(t, payloads, 0)
}

type testRoundTripper struct {
	t         *testing.T
	roundTrip func(*http.Request) (*http.Response, error)
	bodies    []transport.Body
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	body, err := io.ReadAll(req.Body)
	require.NoError(t.t, err)
	req.Body = io.NopCloser(bytes.NewReader(body))
	t.bodies = append(t.bodies, parseRequest(t.t, req.Header, body))
	return t.roundTrip(req)
}

func parseRequest(t *testing.T, headers http.Header, raw []byte) transport.Body {
	t.Helper()

	assert.Equal(t, "v2", headers.Get("DD-Telemetry-API-Version"))
	assert.Equal(t, "application/json", headers.Get("Content-Type"))
	assert.Equal(t, "go", headers.Get("DD-Client-Library-Language"))
	assert.Equal(t, "test-env", headers.Get("DD-Agent-Env"))
	assert.Equal(t, version.Tag, headers.Get("DD-Client-Library-Version"))
	assert.Equal(t, globalconfig.InstrumentationInstallID(), headers.Get("DD-Agent-Install-Id"))
	assert.Equal(t, globalconfig.InstrumentationInstallType(), headers.Get("DD-Agent-Install-Type"))
	assert.Equal(t, globalconfig.InstrumentationInstallTime(), headers.Get("DD-Agent-Install-Time"))

	assert.NotEmpty(t, headers.Get("DD-Agent-Hostname"))

	var body transport.Body
	require.NoError(t, json.Unmarshal(raw, &body))

	assert.Equal(t, string(body.RequestType), headers.Get("DD-Telemetry-Request-Type"))
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

	assert.Equal(t, "v2", body.APIVersion)
	assert.NotZero(t, body.TracerTime)
	assert.LessOrEqual(t, int64(1), body.SeqID)
	assert.Equal(t, globalconfig.RuntimeID(), body.RuntimeID)

	return body
}

func TestClientEnd2End(t *testing.T) {
	tracerConfig := internal.TracerConfig{
		Service: "test-service",
		Env:     "test-env",
		Version: "1.0.0",
	}

	for _, test := range []struct {
		name      string
		when      func(*client)
		roundtrip func(*testing.T, *http.Request) (*http.Response, error)
		expect    func(*testing.T, []transport.Body)
	}{
		{
			name: "app-start",
			when: func(c *client) {
				c.AppStart()
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 1)
				assert.Equal(t, transport.RequestTypeAppStarted, bodies[0].RequestType)
			},
		},
		{
			name: "app-stop",
			when: func(c *client) {
				c.AppStop()
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 1)
				assert.Equal(t, transport.RequestTypeAppClosing, bodies[0].RequestType)
			},
		},
		{
			name: "app-start+app-stop",
			when: func(c *client) {
				c.AppStart()
				c.AppStop()
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 2)
				assert.Equal(t, transport.RequestTypeAppStarted, bodies[0].RequestType)
				assert.Equal(t, transport.RequestTypeAppClosing, bodies[1].RequestType)
			},
		},
		{
			name: "message-batch",
			when: func(c *client) {
				c.RegisterAppConfig("key", "value", OriginCode)
				c.ProductStarted(NamespaceAppSec)
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 1)
				assert.Equal(t, transport.RequestTypeMessageBatch, bodies[0].RequestType)
				batch := bodies[0].Payload.(transport.MessageBatch)
				require.Len(t, batch, 2)
				assert.Equal(t, transport.RequestTypeAppProductChange, batch[0].RequestType)
				assert.Equal(t, transport.RequestTypeAppClientConfigurationChange, batch[1].RequestType)
			},
		},
		{
			name: "fail-agent-endpoint",
			when: func(c *client) {
				c.AppStart()
			},
			roundtrip: func(_ *testing.T, req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Host, "localhost") {
					return nil, errors.New("failed")
				}
				return &http.Response{StatusCode: http.StatusOK}, nil
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 2)
				require.Equal(t, transport.RequestTypeAppStarted, bodies[0].RequestType)
				require.Equal(t, transport.RequestTypeAppStarted, bodies[1].RequestType)
			},
		},
		{
			name: "fail-all-endpoint",
			when: func(c *client) {
				c.AppStart()
			},
			roundtrip: func(_ *testing.T, _ *http.Request) (*http.Response, error) {
				return nil, errors.New("failed")
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 2)
				require.Equal(t, transport.RequestTypeAppStarted, bodies[0].RequestType)
				require.Equal(t, transport.RequestTypeAppStarted, bodies[1].RequestType)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rt := &testRoundTripper{
				t: t,
				roundTrip: func(req *http.Request) (*http.Response, error) {
					if test.roundtrip != nil {
						return test.roundtrip(t, req)
					}
					return &http.Response{StatusCode: http.StatusOK}, nil
				},
			}
			clientConfig := ClientConfig{
				AgentURL: "http://localhost:8126",
				APIKey:   "apikey",
				HTTPClient: &http.Client{
					Timeout:   5 * time.Second,
					Transport: rt,
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
			test.expect(t, rt.bodies)
		})
	}
}

func TestHeartBeatInterval(t *testing.T) {
	startTime := time.Now()
	payloadtimes := make([]time.Duration, 0, 32)
	c, err := NewClient("test-service", "test-env", "1.0.0", ClientConfig{
		AgentURL:          "http://localhost:8126",
		HeartbeatInterval: 2 * time.Second,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &testRoundTripper{
				t: t,
				roundTrip: func(_ *http.Request) (*http.Response, error) {
					payloadtimes = append(payloadtimes, time.Since(startTime))
					startTime = time.Now()
					return &http.Response{
						StatusCode: http.StatusOK,
					}, nil
				},
			},
		},
	})
	require.NoError(t, err)
	defer c.Close()

	for i := 0; i < 10; i++ {
		c.Log(LogError, "test")
		time.Sleep(1 * time.Second)
	}

	// 10 seconds have passed, we should have sent 5 heartbeats

	c.Flush()
	c.Close()

	require.InDelta(t, 5, len(payloadtimes), 1)
	sum := 0.0
	for _, d := range payloadtimes {
		sum += d.Seconds()
	}

	assert.InDelta(t, 2, sum/5, 0.1)
}

func TestSendingFailures(t *testing.T) {
	cfg := ClientConfig{
		AgentURL: "http://localhost:8126",
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &testRoundTripper{
				t: t,
				roundTrip: func(_ *http.Request) (*http.Response, error) {
					return nil, errors.New("failed")
				},
			},
		},
	}

	c, err := newClient(internal.TracerConfig{
		Service: "test-service",
		Env:     "test-env",
		Version: "1.0.0",
	}, defaultConfig(cfg))

	require.NoError(t, err)
	defer c.Close()

	c.Log(LogError, "test")
	c.Flush()

	require.False(t, c.payloadQueue.IsEmpty())
	payload := c.payloadQueue.ReversePeek()
	require.NotNil(t, payload)

	assert.Equal(t, transport.RequestTypeLogs, payload.RequestType())
	logs := payload.(transport.Logs)
	assert.Len(t, logs.Logs, 1)
	assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
	assert.Equal(t, "test", logs.Logs[0].Message)
}

func BenchmarkLogs(b *testing.B) {
	clientConfig := ClientConfig{
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		AgentURL:                  "http://localhost:8126",
		HTTPClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: noopTransport{},
		},
	}

	b.Run("simple", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		b.Cleanup(func() {
			c.Flush()
			c.Close()
		})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogDebug, "this is supposed to be a DEBUG log of representative length with a variable message: "+strconv.Itoa(i%10))
		}
	})

	b.Run("with-tags", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		b.Cleanup(func() {
			c.Flush()
			c.Close()
		})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogWarn, "this is supposed to be a WARN log of representative length", WithTags([]string{"key:" + strconv.Itoa(i%10)}))
		}
	})

	b.Run("with-stacktrace", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		b.Cleanup(func() {
			c.Flush()
			c.Close()
		})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogError, "this is supposed to be a ERROR log of representative length", WithStacktrace())
		}
	})
}

type noopTransport struct{}

func (noopTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK}, nil
}

func BenchmarkParallelLogs(b *testing.B) {
	b.ReportAllocs()
	nbGoroutines := 5 * runtime.NumCPU()

	clientConfig := ClientConfig{
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		FlushInterval:             internal.Range[time.Duration]{Min: time.Second, Max: time.Second},
		AgentURL:                  "http://localhost:8126",

		// Empty transport to avoid sending data to the agent
		HTTPClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: noopTransport{},
		},
	}

	c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
	require.NoError(b, err)

	b.Cleanup(func() {
		c.Flush()
		c.Close()
	})

	b.ResetTimer()
	b.SetParallelism(nbGoroutines)

	var i atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(i.Add(1)) % nbGoroutines
			c.Log(LogDebug, "this is supposed to be a DEBUG log of representative length"+strconv.Itoa(i), WithTags([]string{"key:" + strconv.Itoa(i)}))
		}
	})
}

func benchMetrics(b *testing.B, getHandle, reusedHandle func(*testing.B, func(Client, string) MetricHandle)) {
	for _, bc := range []struct {
		name      string
		newMetric func(Client, string) MetricHandle
	}{
		{"count", func(c Client, name string) MetricHandle { return c.Count(NamespaceGeneral, name, []string{"test:1"}) }},
		{"gauge", func(c Client, name string) MetricHandle { return c.Gauge(NamespaceGeneral, name, []string{"test:1"}) }},
		{"rate", func(c Client, name string) MetricHandle { return c.Rate(NamespaceGeneral, name, []string{"test:1"}) }},
		{"distribution", func(c Client, name string) MetricHandle {
			return c.Distribution(NamespaceGeneral, name, []string{"test:1"})
		}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.Run("get-handle", func(b *testing.B) {
				getHandle(b, bc.newMetric)
			})

			b.Run("handle-reused", func(b *testing.B) {
				reusedHandle(b, bc.newMetric)
			})
		})
	}
}

func BenchmarkMetrics(b *testing.B) {
	clientConfig := ClientConfig{
		Debug:                     true,
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		FlushInterval:             internal.Range[time.Duration]{Min: time.Second, Max: time.Second},
		AgentURL:                  "http://localhost:8126",

		// Empty transport to avoid sending data to the agent
		HTTPClient: &http.Client{
			Transport: noopTransport{},
		},
	}

	benchMetrics(b, func(b *testing.B, f func(Client, string) MetricHandle) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		b.Cleanup(func() {
			c.Flush()
			c.Close()
		})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f(c, "init_time").Submit(1)
		}
	}, func(b *testing.B, f func(Client, string) MetricHandle) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		b.Cleanup(func() {
			c.Flush()
			c.Close()
		})

		b.ResetTimer()
		handle := f(c, "init_time")
		for i := 0; i < b.N; i++ {
			handle.Submit(1)
		}
	})
}

func BenchmarkParallelMetrics(b *testing.B) {
	nbGoroutines := 5 * runtime.NumCPU()
	clientConfig := ClientConfig{
		Debug:                     true,
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		FlushInterval:             internal.Range[time.Duration]{Min: time.Second, Max: time.Second},
		AgentURL:                  "http://localhost:8126",

		// Empty transport to avoid sending data to the agent
		HTTPClient: &http.Client{
			Transport: noopTransport{},
		},
	}

	benchMetrics(b, func(b *testing.B, metric func(Client, string) MetricHandle) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		b.Cleanup(func() {
			c.Flush()
			c.Close()
		})

		b.ResetTimer()
		b.SetParallelism(nbGoroutines)

		var i atomic.Int64
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				metricName := "init_time_" + strconv.Itoa(int(i.Add(1))%nbGoroutines)
				metric(c, metricName).Submit(1)
			}
		})
	}, func(b *testing.B, metric func(Client, string) MetricHandle) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		b.Cleanup(func() {
			c.Flush()
			c.Close()
		})

		b.ResetTimer()
		b.SetParallelism(nbGoroutines)

		handles := make([]MetricHandle, nbGoroutines)
		for i := 0; i < nbGoroutines; i++ {
			handles[i] = metric(c, "init_time_"+strconv.Itoa(i))
		}

		var i atomic.Int32
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				handles[int(i.Add(1))%nbGoroutines].Submit(1)
			}
		})
	})
}
