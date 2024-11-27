// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package transport

import (
	"runtime"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	tracerversion "gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

// Body is the common high-level structure encapsulating a telemetry request body
type Body struct {
	APIVersion  string      `json:"api_version"`
	RequestType RequestType `json:"request_type"`
	TracerTime  int64       `json:"tracer_time"`
	RuntimeID   string      `json:"runtime_id"`
	SeqID       int64       `json:"seq_id"`
	Debug       bool        `json:"debug"`
	Payload     interface{} `json:"payload"`
	Application Application `json:"application"`
	Host        Host        `json:"host"`
}

func NewBody(service, env, version string) *Body {
	return &Body{
		APIVersion: "v2",
		RuntimeID:  globalconfig.RuntimeID(),
		Application: Application{
			ServiceName:     service,
			Env:             env,
			ServiceVersion:  version,
			TracerVersion:   tracerversion.Tag,
			LanguageName:    "go",
			LanguageVersion: runtime.Version(),
		},
		Host: Host{
			Hostname:      hostname.Get(),
			OS:            osinfo.OSName(),
			OSVersion:     osinfo.OSVersion(),
			Architecture:  osinfo.Architecture(),
			KernelName:    osinfo.KernelName(),
			KernelRelease: osinfo.KernelRelease(),
			KernelVersion: osinfo.KernelVersion(),
		},
	}
}
