// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import "context"

type cdnSource struct {
	poller *cdnPoller
}

func startWithCDN(config ProviderConfig, cdnConfig resolvedFeatureFlagCDNConfig) (*DatadogProvider, error) {
	provider := newDatadogProvider(config)
	provider.sourceMode = FeatureFlagSourceModeCDN

	source, err := newCDNSource(provider, cdnConfig)
	if err != nil {
		return nil, err
	}
	provider.cdnSource = source
	source.start()
	return provider, nil
}

func startWithOffline(config ProviderConfig, _ FeatureFlagOfflineConfig) (*DatadogProvider, error) {
	provider := newDatadogProvider(config)
	provider.sourceMode = FeatureFlagSourceModeOffline
	return provider, nil
}

func newCDNSource(provider *DatadogProvider, cdnConfig resolvedFeatureFlagCDNConfig) (*cdnSource, error) {
	poller, err := newCDNPoller(cdnPollerConfig{
		baseURL:        cdnConfig.BaseURL,
		apiKey:         cdnConfig.APIKey,
		pollInterval:   cdnConfig.PollInterval,
		requestTimeout: cdnConfig.RequestTimeout,
		httpClient:     cdnConfig.HTTPClient,
		apply:          provider.updateConfiguration,
	})
	if err != nil {
		return nil, err
	}
	return &cdnSource{poller: poller}, nil
}

func (s *cdnSource) start() {
	s.poller.start()
}

func (s *cdnSource) stop(ctx context.Context) error {
	return s.poller.stop(ctx)
}
