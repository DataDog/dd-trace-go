// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package telemetry

import (
	"net/http"
	"net/url"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type Option func(*Client)

func WithNamespace(name Namespace) Option {
	return func(client *Client) {
		client.Namespace = name
	}
}
func WithEnv(env string) Option {
	return func(client *Client) {
		client.Env = env
	}
}
func WithService(service string) Option {
	return func(client *Client) {
		client.Service = service
	}
}
func WithVersion(version string) Option {
	return func(client *Client) {
		client.Version = version
	}
}
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.Client = httpClient
	}
}
func WithAPIKey(v string) Option {
	return func(client *Client) {
		client.APIKey = v
	}
}
func WithURL(agentless bool, agentURL string) Option {
	return func(client *Client) {
		if agentless {
			// need to check that there is a valid api key
			if client.APIKey == "" {
				if v := os.Getenv("DD_API_KEY"); v != "" {
					WithAPIKey(v)(client)
				} else {
					log.Warn("Agentless is turned out, but valid DD API key was not found. Not starting telemetry")
					client.Disabled = true
				}
			}
			client.URL = "https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry"
		} else {
			// TODO: check agent /info endpoint to see if the agent is
			// sufficiently recent to support this endpoint? overkill?
			u, err := url.Parse(agentURL)
			if err == nil {
				u.Path = "/telemetry/proxy/api/v2/apmtelemetry"
				client.URL = u.String()
			} else {
				log.Warn("Agent URL %s is invalid, not starting telemetry", agentURL)
				client.Disabled = true
			}
		}
	}
}
func WithLogger(logger interface {
	Printf(msg string, args ...interface{})
}) Option {
	return func(client *Client) {
		client.Logger = logger
	}
}
func defaultClient() (client *Client) {
	client = new(Client)
	return client
}
