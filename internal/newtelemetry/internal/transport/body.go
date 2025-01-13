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

// Application is identifying information about the app itself
type Application struct {
	ServiceName     string `json:"service_name"`
	Env             string `json:"env"`
	ServiceVersion  string `json:"service_version"`
	TracerVersion   string `json:"tracer_version"`
	LanguageName    string `json:"language_name"`
	LanguageVersion string `json:"language_version"`
}

// Host is identifying information about the host on which the app
// is running
type Host struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	OSVersion     string `json:"os_version,omitempty"`
	Architecture  string `json:"architecture"`
	KernelName    string `json:"kernel_name"`
	KernelRelease string `json:"kernel_release"`
	KernelVersion string `json:"kernel_version"`
}

// Body is the common high-level structure encapsulating a telemetry request body
type Body struct {
	APIVersion  string      `json:"api_version"`
	RequestType RequestType `json:"request_type"`
	TracerTime  int64       `json:"tracer_time"`
	RuntimeID   string      `json:"runtime_id"`
	SeqID       int64       `json:"seq_id"`
	Debug       bool        `json:"debug"`
	Payload     Payload     `json:"payload"`
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
