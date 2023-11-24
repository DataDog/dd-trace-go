// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

type configData struct {
	Action        string    `json:"action"`
	ServiceTarget target    `json:"service_target"`
	LibConfig     libConfig `json:"lib_config"`
}

type target struct {
	Service string `json:"service"`
	Env     string `json:"env"`
}

type libConfig struct {
	SamplingRate *float64    `json:"tracing_sampling_rate,omitempty"`
	HeaderTags   *headerTags `json:"tracing_header_tags,omitempty"`
	Tags         *tags       `json:"tracing_tags,omitempty"`
}

type headerTags []headerTag

type headerTag struct {
	Header  string `json:"header"`
	TagName string `json:"tag_name"`
}

func (hts *headerTags) toSlice() *[]string {
	if hts == nil {
		return nil
	}
	s := make([]string, len(*hts))
	for i, ht := range *hts {
		s[i] = ht.toString()
	}
	return &s
}

func (ht headerTag) toString() string {
	var sb strings.Builder
	sb.WriteString(ht.Header)
	sb.WriteString(":")
	sb.WriteString(ht.TagName)
	return sb.String()
}

type tags []string

func (t *tags) toMap() *map[string]interface{} {
	if t == nil {
		return nil
	}
	m := make(map[string]interface{}, len(*t))
	for _, tag := range *t {
		if kv := strings.SplitN(tag, ":", 2); len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return &m
}

// onRemoteConfigUpdate is a remote config callaback responsible for processing APM_TRACING RC-product updates.
func (t *tracer) onRemoteConfigUpdate(u remoteconfig.ProductUpdate) map[string]state.ApplyStatus {
	statuses := map[string]state.ApplyStatus{}
	if u == nil {
		return statuses
	}
	var telemConfigs []telemetry.Configuration
	for path, raw := range u {
		if raw == nil {
			continue
		}
		log.Debug("Processing config from RC. Path: %s. Raw: %s", path, raw)
		var c configData
		if err := json.Unmarshal(raw, &c); err != nil {
			log.Debug("Error while unmarshalling payload for %s: %v. Configuration won't be applied.", path, err)
			statuses[path] = state.ApplyStatus{State: state.ApplyStateError, Error: err.Error()}
			continue
		}
		if c.ServiceTarget.Service != t.config.serviceName {
			log.Debug("Skipping config for service %s. Current service is %s", c.ServiceTarget.Service, t.config.serviceName)
			statuses[path] = state.ApplyStatus{State: state.ApplyStateError, Error: "service mismatch"}
			continue
		}
		if c.ServiceTarget.Env != t.config.env {
			log.Debug("Skipping config for env %s. Current env is %s", c.ServiceTarget.Env, t.config.env)
			statuses[path] = state.ApplyStatus{State: state.ApplyStateError, Error: "env mismatch"}
			continue
		}
		statuses[path] = state.ApplyStatus{State: state.ApplyStateAcknowledged}
		updated := t.config.traceSampleRate.handleRC(c.LibConfig.SamplingRate)
		if updated {
			telemConfigs = append(telemConfigs, t.config.traceSampleRate.toTelemetry())
		}
		updated = t.config.headerAsTags.handleRC(c.LibConfig.HeaderTags.toSlice())
		if updated {
			telemConfigs = append(telemConfigs, t.config.headerAsTags.toTelemetry())
		}
		updated = t.config.globalTags.handleRC(c.LibConfig.Tags.toMap())
		if updated {
			telemConfigs = append(telemConfigs, t.config.globalTags.toTelemetry())
		}
	}
	if len(telemConfigs) > 0 {
		log.Debug("Reporting %d configuration changes to telemetry", len(telemConfigs))
		telemetry.GlobalClient.ConfigChange(telemConfigs)
	}
	return statuses
}

// startRemoteConfig starts the remote config client
// and registers the APM_TRACING product and its callback.
func (t *tracer) startRemoteConfig(rcConfig remoteconfig.ClientConfig) error {
	err := remoteconfig.Start(rcConfig)
	if err != nil {
		return err
	}
	return remoteconfig.Subscribe(
		state.ProductAPMTracing,
		t.onRemoteConfigUpdate,
		remoteconfig.APMTracingSampleRate,
		remoteconfig.APMTracingHTTPHeaderTags,
		remoteconfig.APMTracingCustomTags,
	)
}
