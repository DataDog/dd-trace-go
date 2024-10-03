// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	DefaultMaxRetries int           = 5
	DefaultBackoff    time.Duration = 150 * time.Millisecond
)

type (
	Client interface {
		GetSettings() (*SettingsResponseData, error)
		GetEarlyFlakeDetectionData() (*EfdResponseData, error)
		GetCommits(localCommits []string) ([]string, error)
		SendPackFiles(packFiles []string) (bytes int64, err error)
	}

	client struct {
		id                 string
		agentless          bool
		baseURL            string
		environment        string
		serviceName        string
		workingDirectory   string
		repositoryURL      string
		commitSha          string
		branchName         string
		testConfigurations testConfigurations
		headers            map[string]string
		handler            *RequestHandler
	}

	testConfigurations struct {
		OsPlatform          string            `json:"os.platform,omitempty"`
		OsVersion           string            `json:"os.version,omitempty"`
		OsArchitecture      string            `json:"os.architecture,omitempty"`
		RuntimeName         string            `json:"runtime.name,omitempty"`
		RuntimeArchitecture string            `json:"runtime.architecture,omitempty"`
		RuntimeVersion      string            `json:"runtime.version,omitempty"`
		Custom              map[string]string `json:"custom,omitempty"`
	}
)

var _ Client = &client{}

func NewClientWithServiceName(serviceName string) Client {
	ciTags := utils.GetCITags()

	// get the environment
	environment := os.Getenv("DD_ENV")
	if environment == "" {
		environment = "none"
	}

	// get the service name
	if serviceName == "" {
		serviceName = os.Getenv("DD_SERVICE")
		if serviceName == "" {
			if repoURL, ok := ciTags[constants.GitRepositoryURL]; ok {
				// regex to sanitize the repository url to be used as a service name
				repoRegex := regexp.MustCompile(`(?m)/([a-zA-Z0-9\-_.]*)$`)
				matches := repoRegex.FindStringSubmatch(repoURL)
				if len(matches) > 1 {
					repoURL = strings.TrimSuffix(matches[1], ".git")
				}
				serviceName = repoURL
			}
		}
	}

	// get all custom configuration (test.configuration.*)
	var customConfiguration map[string]string
	if v := os.Getenv("DD_TAGS"); v != "" {
		prefix := "test.configuration."
		for k, v := range internal.ParseTagString(v) {
			if strings.HasPrefix(k, prefix) {
				if customConfiguration == nil {
					customConfiguration = map[string]string{}
				}

				customConfiguration[strings.TrimPrefix(k, prefix)] = v
			}
		}
	}

	// create default http headers and get base url
	defaultHeaders := map[string]string{}
	var baseURL string
	var requestHandler *RequestHandler

	agentlessEnabled := internal.BoolEnv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, false)
	if agentlessEnabled {
		// Agentless mode is enabled.
		APIKeyValue := os.Getenv(constants.APIKeyEnvironmentVariable)
		if APIKeyValue == "" {
			log.Error("An API key is required for agentless mode. Use the DD_API_KEY env variable to set it")
			return nil
		}

		defaultHeaders["dd-api-key"] = APIKeyValue

		// Check for a custom agentless URL.
		agentlessURL := os.Getenv(constants.CIVisibilityAgentlessURLEnvironmentVariable)

		if agentlessURL == "" {
			// Use the standard agentless URL format.
			site := "datadoghq.com"
			if v := os.Getenv("DD_SITE"); v != "" {
				site = v
			}

			baseURL = fmt.Sprintf("https://api.%s", site)
		} else {
			// Use the custom agentless URL.
			baseURL = agentlessURL
		}

		requestHandler = NewRequestHandler()
	} else {
		// Use agent mode with the EVP proxy.
		defaultHeaders["X-Datadog-EVP-Subdomain"] = "api"

		agentURL := internal.AgentURLFromEnv()
		if agentURL.Scheme == "unix" {
			// If we're connecting over UDS we can just rely on the agent to provide the hostname
			log.Debug("connecting to agent over unix, do not set hostname on any traces")
			dialer := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}
			requestHandler = NewRequestHandlerWithClient(&http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyFromEnvironment,
					DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						return dialer.DialContext(ctx, "unix", (&net.UnixAddr{
							Name: agentURL.Path,
							Net:  "unix",
						}).String())
					},
					MaxIdleConns:          100,
					IdleConnTimeout:       90 * time.Second,
					TLSHandshakeTimeout:   10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
				},
				Timeout: 10 * time.Second,
			})
			agentURL = &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("UDS_%s", strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(agentURL.Path)),
			}
		} else {
			requestHandler = NewRequestHandler()
		}

		baseURL = agentURL.String()
	}

	// create random id (the backend associate all transactions with the client request)
	id := fmt.Sprint(rand.Uint64() & math.MaxInt64)
	defaultHeaders["trace_id"] = id
	defaultHeaders["parent_id"] = id

	return &client{
		id:               id,
		agentless:        agentlessEnabled,
		baseURL:          baseURL,
		environment:      environment,
		serviceName:      serviceName,
		workingDirectory: ciTags[constants.CIWorkspacePath],
		repositoryURL:    ciTags[constants.GitRepositoryURL],
		commitSha:        ciTags[constants.GitCommitSHA],
		branchName:       ciTags[constants.GitBranch],
		testConfigurations: testConfigurations{
			OsPlatform:     ciTags[constants.OSPlatform],
			OsVersion:      ciTags[constants.OSVersion],
			OsArchitecture: ciTags[constants.OSArchitecture],
			RuntimeName:    ciTags[constants.RuntimeName],
			RuntimeVersion: ciTags[constants.RuntimeVersion],
			Custom:         customConfiguration,
		},
		headers: defaultHeaders,
		handler: requestHandler,
	}
}

func NewClient() Client {
	return NewClientWithServiceName("")
}

func (c *client) getURLPath(urlPath string) string {
	if c.agentless {
		return fmt.Sprintf("%s/%s", c.baseURL, urlPath)
	}

	return fmt.Sprintf("%s/%s/%s", c.baseURL, "evp_proxy/v2", urlPath)
}

func (c *client) getPostRequestConfig(url string, body interface{}) *RequestConfig {
	return &RequestConfig{
		Method:     "POST",
		URL:        c.getURLPath(url),
		Headers:    c.headers,
		Body:       body,
		Format:     FormatJSON,
		Compressed: false,
		Files:      nil,
		MaxRetries: DefaultMaxRetries,
		Backoff:    DefaultBackoff,
	}
}
