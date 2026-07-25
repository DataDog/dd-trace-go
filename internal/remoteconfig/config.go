// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package remoteconfig

import (
	"net/http"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const envPollIntervalSec = "DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS"

// ClientConfig contains the required values to configure a remoteconfig client
type ClientConfig struct {
	// The address at which the agent is listening for remoteconfig update requests on
	AgentURL string
	// The semantic version of the user's application
	AppVersion string
	// The env this tracer is running in
	Env string
	// The time interval between two client polls to the agent for updates
	PollInterval time.Duration
	// The tracer's runtime id
	RuntimeID string
	// The name of the user's application
	ServiceName string
	// The semantic version of the tracer
	TracerVersion string
	// The base TUF root metadata file
	TUFRoot string
	// HTTP is the HTTP client used to receive config updates
	HTTP *http.Client
}

// DefaultClientConfig returns the default remote config client configuration
func DefaultClientConfig() ClientConfig {
	snapshot := internalconfig.ResolveRemoteConfigSnapshot()
	return ClientConfig{
		Env:           snapshot.Env,
		HTTP:          &http.Client{Timeout: 10 * time.Second},
		PollInterval:  snapshot.PollInterval,
		RuntimeID:     globalconfig.RuntimeID(),
		ServiceName:   globalconfig.ServiceName(),
		TracerVersion: version.Tag,
		TUFRoot:       snapshot.TUFRoot,
	}
}
