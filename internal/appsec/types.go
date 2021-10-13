// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"net/http"
	"time"
)

type (
	// Config is the AppSec configuration.
	Config struct {
		// Client is the HTTP client to use to perform HTTP requests to the agent. This value is mandatory.
		Client *http.Client
		// AgentURL is the datadog agent URL the API client should use.
		AgentURL string
		// ServiceConfig is the information about the running service we currently protect.
		Service ServiceConfig
		// Tags is the list of tags that should be added to security events (eg. pid, os name, etc.).
		Tags map[string]interface{}
		// Hostname of the machine we run in.
		Hostname string
		// Version of the Go client library
		Version string

		// MaxBatchLen is the maximum batch length the event batching loop should use. The event batch is sent when
		// this length is reached. Defaults to 1024.
		MaxBatchLen int
		// MaxBatchStaleTime is the maximum amount of time events are kept in the batch. This allows to send the batch
		// after this amount of time even if the maximum batch length is not reached yet. Defaults to 1 second.
		MaxBatchStaleTime time.Duration

		// rules loaded via the env var DD_APPSEC_RULES. When not set, the builtin rules will be used.
		rules []byte
	}

	// ServiceConfig is the optional context about the running service.
	ServiceConfig struct {
		// Name of the service.
		Name string
		// Version of the service.
		Version string
		// Environment of the service (eg. dev, staging, prod, etc.)
		Environment string
	}
)
