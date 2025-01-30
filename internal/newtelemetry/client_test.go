// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
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
		expect       func(*testing.T, []transport.Payload)
	}{
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
				HeartbeatInterval:         time.Nanosecond,
				ExtendedHeartbeatInterval: time.Nanosecond,
			},
			when: func(c *client) {
				c.AddAppConfig("key", "value", OriginDefault)
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
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
				c.AddAppConfig("key", "value", OriginDefault)
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
			name: "configuration-default",
			when: func(c *client) {
				c.AddAppConfig("key", "value", OriginDefault)
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
				assert.Len(t, batch.Payload, 2)
				for _, payload := range batch.Payload {
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
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.MessageBatch{}, payload)
				batch := payload.(transport.MessageBatch)
				assert.Len(t, batch.Payload, 3)
				for _, payload := range batch.Payload {
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
				c.appStart()
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
				c.appStart()
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
				c.appStart()
				c.AddAppConfig("key", "value", OriginDefault)
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
				c.appStart()
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
				c.appStart()
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
				c.appStop()
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
				c.Log(LogError, "test", WithTags(map[string]string{"key": "value"}))
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
				c.Log(LogError, "test", WithTags(map[string]string{"key": "value", "key2": "value2"}))
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
				c.Log(LogError, "test", WithTags(map[string]string{"key": "value", "key2": "value2"}))
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
				assert.Contains(t, logs.Logs[0].StackTrace, "internal/newtelemetry/client_test.go")
			},
		},
		{
			name: "single-log-with-stacktrace-and-tags",
			when: func(c *client) {
				c.Log(LogError, "test", WithStacktrace(), WithTags(map[string]string{"key": "value", "key2": "value2"}))
			},
			expect: func(t *testing.T, payloads []transport.Payload) {
				payload := payloads[0]
				require.IsType(t, transport.Logs{}, payload)
				logs := payload.(transport.Logs)
				require.Len(t, logs.Logs, 1)
				assert.Equal(t, transport.LogLevelError, logs.Logs[0].Level)
				assert.Equal(t, "test", logs.Logs[0].Message)
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
				c.Count(NamespaceTracers, "init_time", map[string]string{"test": "1"}).Submit(1)
				c.Count(NamespaceTracers, "init_time", map[string]string{"test": "2"}).Submit(2)
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
				c.Gauge(NamespaceTracers, "init_time", map[string]string{"test": "1"}).Submit(1)
				c.Gauge(NamespaceTracers, "init_time", map[string]string{"test": "2"}).Submit(2)
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
				rate.intervalStart = rate.intervalStart.Add(-time.Second) // So the rate is not +Infinity because the interval is zero
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
				c.Distribution(NamespaceTracers, "init_time", map[string]string{"test": "1"}).Submit(1)
				c.Distribution(NamespaceTracers, "init_time", map[string]string{"test": "2"}).Submit(2)
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
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := defaultConfig(test.clientConfig)
			config.AgentURL = "http://localhost:8126"
			config.DependencyLoader = test.clientConfig.DependencyLoader             // Don't use the default dependency loader
			config.InternalMetricsEnabled = test.clientConfig.InternalMetricsEnabled // only enabled internal metrics when explicitly set
			config.InternalMetricsEnabled = false
			c, err := newClient(tracerConfig, config)
			require.NoError(t, err)
			defer c.Close()

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
				c.appStart()
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 1)
				assert.Equal(t, transport.RequestTypeAppStarted, bodies[0].RequestType)
			},
		},
		{
			name: "app-stop",
			when: func(c *client) {
				c.appStop()
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 1)
				assert.Equal(t, transport.RequestTypeAppClosing, bodies[0].RequestType)
			},
		},
		{
			name: "app-start+app-stop",
			when: func(c *client) {
				c.appStart()
				c.appStop()
			},
			expect: func(t *testing.T, bodies []transport.Body) {
				require.Len(t, bodies, 2)
				assert.Equal(t, transport.RequestTypeAppStarted, bodies[0].RequestType)
				assert.Equal(t, transport.RequestTypeAppClosing, bodies[1].RequestType)
			},
		},
		{
			name: "fail-agent-endpoint",
			when: func(c *client) {
				c.appStart()
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
				c.appStart()
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
	}

	b.Run("simple", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogDebug, "this is supposed to be a DEBUG log of representative length with a variable message: "+strconv.Itoa(i%10))
		}
	})

	b.Run("with-tags", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Log(LogWarn, "this is supposed to be a WARN log of representative length", WithTags(map[string]string{"key": strconv.Itoa(i % 10)}))
		}
	})

	b.Run("with-stacktrace", func(b *testing.B) {
		b.ReportAllocs()
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
	b.ReportAllocs()
	nbSameLogs := 10
	nbDifferentLogs := 100
	nbGoroutines := 25

	clientConfig := ClientConfig{
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		FlushInterval:             Range[time.Duration]{Min: time.Second, Max: time.Second},
		AgentURL:                  "http://localhost:8126",

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

	b.ReportMetric(float64(b.Elapsed().Nanoseconds()/int64(nbGoroutines*nbDifferentLogs*nbSameLogs*b.N)), "ns/log")
}

func BenchmarkMetrics(b *testing.B) {
	b.ReportAllocs()
	clientConfig := ClientConfig{
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		AgentURL:                  "http://localhost:8126",
	}

	b.Run("count+get-handle", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Count(NamespaceTracers, "init_time", nil).Submit(1)
		}
	})

	b.Run("count+handle-reused", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		handle := c.Count(NamespaceTracers, "init_time", nil)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			handle.Submit(1)
		}
	})

	b.Run("gauge+get-handle", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Gauge(NamespaceTracers, "init_time", nil).Submit(1)
		}
	})

	b.Run("gauge+handle-reused", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		handle := c.Gauge(NamespaceTracers, "init_time", nil)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			handle.Submit(1)
		}
	})

	b.Run("rate+get-handle", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Rate(NamespaceTracers, "init_time", nil).Submit(1)
		}
	})

	b.Run("rate+handle-reused", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		handle := c.Rate(NamespaceTracers, "init_time", nil)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			handle.Submit(1)
		}
	})

	b.Run("distribution+get-handle", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.Distribution(NamespaceTracers, "init_time", nil).Submit(1)
		}
	})

	b.Run("distribution+handle-reused", func(b *testing.B) {
		b.ReportAllocs()
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		handle := c.Distribution(NamespaceTracers, "init_time", nil)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			handle.Submit(1)
		}
	})
}

func BenchmarkWorstCaseScenarioFloodMetrics(b *testing.B) {
	b.ReportAllocs()
	nbSameMetric := 10
	nbDifferentMetrics := 100
	nbGoroutines := 25

	clientConfig := ClientConfig{
		HeartbeatInterval:         time.Hour,
		ExtendedHeartbeatInterval: time.Hour,
		FlushInterval:             Range[time.Duration]{Min: time.Second, Max: time.Second},
		DistributionsSize:         Range[int]{256, -1},
		AgentURL:                  "http://localhost:8126",

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

	b.Run("get-handle", func(b *testing.B) {
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
					for j := 0; j < nbDifferentMetrics; j++ {
						for k := 0; k < nbSameMetric; k++ {
							c.Count(NamespaceTracers, "init_time", map[string]string{"test": "1"}).Submit(1)
						}
					}
				}()
			}

			wg.Wait()
		}

		b.ReportMetric(float64(b.Elapsed().Nanoseconds()/int64(nbGoroutines*nbDifferentMetrics*nbSameMetric*b.N)), "ns/point")
	})

	b.Run("handle-reused", func(b *testing.B) {
		c, err := NewClient("test-service", "test-env", "1.0.0", clientConfig)
		require.NoError(b, err)

		defer c.Close()

		b.ResetTimer()

		for x := 0; x < b.N; x++ {
			var wg sync.WaitGroup

			handle := c.Count(NamespaceTracers, "init_time", map[string]string{"test": "1"})
			for i := 0; i < nbGoroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < nbDifferentMetrics; j++ {

						for k := 0; k < nbSameMetric; k++ {
							handle.Submit(1)
						}
					}
				}()
			}

			wg.Wait()
		}

		b.ReportMetric(float64(b.Elapsed().Nanoseconds()/int64(nbGoroutines*nbDifferentMetrics*nbSameMetric*b.N)), "ns/point")
	})

}
