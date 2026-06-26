// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

const (
	envFeatureFlagSourceMode               = "DD_FLAGGING_SOURCE_MODE"
	envFeatureFlagCDNBaseURL               = "DD_FLAGGING_CDN_BASE_URL"
	envFeatureFlagCDNPollIntervalSeconds   = "DD_FLAGGING_CDN_POLL_INTERVAL_SECONDS"
	envFeatureFlagCDNRequestTimeoutSeconds = "DD_FLAGGING_CDN_REQUEST_TIMEOUT_SECONDS"

	defaultFeatureFlagCDNSite           = "datadoghq.com"
	defaultFeatureFlagCDNPollInterval   = 60 * time.Second
	defaultFeatureFlagCDNRequestTimeout = 5 * time.Second
)

// FeatureFlagSourceMode identifies the source used to deliver UFC bytes.
type FeatureFlagSourceMode string

const (
	// FeatureFlagSourceModeCDN selects direct CDN-backed UFC delivery.
	FeatureFlagSourceModeCDN FeatureFlagSourceMode = "cdn"
	// FeatureFlagSourceModeRemoteConfig selects the existing Datadog Remote Configuration path.
	FeatureFlagSourceModeRemoteConfig FeatureFlagSourceMode = "remote_config"
	// FeatureFlagSourceModeOffline reserves future startup-byte delivery without network work.
	FeatureFlagSourceModeOffline FeatureFlagSourceMode = "offline"
)

// FeatureFlagSourceConfig groups source-specific provider configuration.
type FeatureFlagSourceConfig struct {
	Mode    FeatureFlagSourceMode
	CDN     FeatureFlagCDNConfig
	Offline FeatureFlagOfflineConfig
}

// FeatureFlagCDNConfig contains CDN source configuration.
type FeatureFlagCDNConfig struct {
	BaseURL        string
	PollInterval   time.Duration
	RequestTimeout time.Duration
	HTTPClient     *http.Client
}

// FeatureFlagOfflineConfig reserves future no-network startup bytes mode.
type FeatureFlagOfflineConfig struct {
	Payload []byte
}

type resolvedFeatureFlagSourceConfig struct {
	Mode    FeatureFlagSourceMode
	CDN     resolvedFeatureFlagCDNConfig
	Offline FeatureFlagOfflineConfig
}

type resolvedFeatureFlagCDNConfig struct {
	BaseURL        string
	PollInterval   time.Duration
	RequestTimeout time.Duration
	HTTPClient     *http.Client
	APIKey         string
}

func resolveFeatureFlagSourceConfig(config FeatureFlagSourceConfig) (resolvedFeatureFlagSourceConfig, error) {
	mode := config.Mode
	if mode == "" {
		mode = FeatureFlagSourceMode(os.Getenv(envFeatureFlagSourceMode))
	}
	if mode == "" {
		mode = FeatureFlagSourceModeCDN
	}

	resolved := resolvedFeatureFlagSourceConfig{
		Mode:    mode,
		Offline: config.Offline,
	}

	switch mode {
	case FeatureFlagSourceModeCDN:
		resolved.CDN = resolveFeatureFlagCDNConfig(config.CDN)
	case FeatureFlagSourceModeRemoteConfig, FeatureFlagSourceModeOffline:
	default:
		return resolvedFeatureFlagSourceConfig{}, fmt.Errorf("unsupported feature flag source mode %q", mode)
	}
	return resolved, nil
}

func resolveFeatureFlagCDNConfig(config FeatureFlagCDNConfig) resolvedFeatureFlagCDNConfig {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv(envFeatureFlagCDNBaseURL)
	}
	if baseURL == "" {
		baseURL = defaultFeatureFlagCDNBaseURL()
	}

	pollInterval := config.PollInterval
	if pollInterval == 0 {
		pollInterval = secondsEnv(envFeatureFlagCDNPollIntervalSeconds, defaultFeatureFlagCDNPollInterval)
	}
	if pollInterval < 0 {
		pollInterval = defaultFeatureFlagCDNPollInterval
	}

	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = secondsEnv(envFeatureFlagCDNRequestTimeoutSeconds, defaultFeatureFlagCDNRequestTimeout)
	}
	if requestTimeout <= 0 {
		requestTimeout = defaultFeatureFlagCDNRequestTimeout
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return resolvedFeatureFlagCDNConfig{
		BaseURL:        baseURL,
		PollInterval:   pollInterval,
		RequestTimeout: requestTimeout,
		HTTPClient:     httpClient,
		APIKey:         env.Get("DD_API_KEY"),
	}
}

func secondsEnv(name string, fallback time.Duration) time.Duration {
	seconds := internal.FloatEnv(name, fallback.Seconds())
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds * float64(time.Second))
}

func defaultFeatureFlagCDNBaseURL() string {
	site := env.Get("DD_SITE")
	if site == "" {
		site = defaultFeatureFlagCDNSite
	}
	return fmt.Sprintf("https://feature-flags.%s", site)
}
