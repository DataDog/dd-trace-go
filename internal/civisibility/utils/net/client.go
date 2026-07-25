// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	// DefaultMaxRetries is the default number of retries for a request.
	DefaultMaxRetries int = 3
	// DefaultBackoff is the default backoff time for a request.
	DefaultBackoff time.Duration = 100 * time.Millisecond
)

type (
	// Client is an interface for sending requests to the Datadog backend.
	Client interface {
		GetSettings() (*SettingsResponseData, error)
		GetKnownTests() (*KnownTestsResponseData, error)
		GetCommits(localCommits []string) ([]string, error)
		SendPackFiles(commitSha string, packFiles []string) (bytes int64, err error)
		SendCoveragePayload(ciTestCovPayload io.Reader) error
		SendCoveragePayloadWithFormat(ciTestCovPayload io.Reader, format string) error
		SendCoverageReport(report io.Reader, format string) error
		GetSkippableTests() (*SkippableTestsResponse, error)
		GetTestManagementTests() (*TestManagementTestsResponseDataModules, error)
		SendLogs(logsPayload io.Reader) error
	}

	// coverageClient is an interface for sending coverage reports to the Datadog backend.
	coverageClient interface {
		Client
		SetCoverageFlags([]string)
	}

	// client is a client for sending requests to the Datadog backend.
	client struct {
		id                  string
		agentless           bool
		baseURL             string
		environment         string
		serviceName         string
		workingDirectory    string
		repositoryURL       string
		commitSha           string
		commitMessage       string
		headCommitSha       string
		headCommitMessage   string
		branchName          string
		testConfigurations  testConfigurations
		coverageReportFlags []string
		// readCacheScopeIdentity stores the short-lived read-cache scope derived from already-resolved CI tags.
		readCacheScopeIdentity readCacheScopeIdentity
		headers                map[string]string
		handler                *RequestHandler
	}

	// testConfigurations represents the test configurations.
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

var (
	_ Client         = &client{}
	_ coverageClient = &client{}

	// telemetryInit is used to initialize the telemetry client.
	telemetryInit           sync.Once
	telemetryPreparedClient telemetry.Client
	telemetryPreparedEvents []internalconfig.ConfigEvent
	telemetryPreparedConfig []telemetry.Configuration
	telemetryGlobalClient   = telemetry.GlobalClient
)

func resetCIVisibilityTelemetryForTesting() {
	telemetryInit = sync.Once{}
	telemetryPreparedClient = nil
	telemetryPreparedEvents = nil
	telemetryPreparedConfig = nil
}

// NewClientWithServiceNameAndSubdomain creates a new client with the given service name and subdomain.
func NewClientWithServiceNameAndSubdomain(serviceName, subdomain string) Client {
	client, report := PrepareClientWithServiceNameAndSubdomain(serviceName, subdomain)
	report()
	return client
}

// PrepareClientWithServiceNameAndSubdomain constructs a client and defers all
// configuration and telemetry publication until the returned reporter runs.
func PrepareClientWithServiceNameAndSubdomain(serviceName, subdomain string) (Client, func()) {
	config, clientConfigEvents := internalconfig.PrepareCIVisibilityClientConfig()
	ciTags, reportCITags := utils.PrepareCITags()
	var agentConfigEvents []internalconfig.ConfigEvent
	var reportTelemetry func()
	var reportOnce sync.Once
	report := func() {
		reportOnce.Do(func() {
			internalconfig.ReportCIVisibilityConfigEvents(clientConfigEvents)
			reportCITags()
			internalconfig.ReportCIVisibilityConfigEvents(agentConfigEvents)
			if reportTelemetry != nil {
				reportTelemetry()
			}
		})
	}

	environment := config.Environment

	// get the service name
	if serviceName == "" {
		serviceName = config.Service
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
	customConfiguration := config.CustomTestConfigurations

	// create default http headers and get base url
	defaultHeaders := map[string]string{}
	var baseURL string
	var requestHandler *RequestHandler
	var agentURL *url.URL
	var apiKeyValue string

	agentlessEnabled := config.AgentlessEnabled
	if agentlessEnabled {
		// Agentless mode is enabled.
		apiKeyValue = config.APIKey
		if apiKeyValue == "" {
			log.Error("An API key is required for agentless mode. Use the DD_API_KEY env variable to set it")
			return nil, report
		}

		defaultHeaders["dd-api-key"] = apiKeyValue

		// Check for a custom agentless URL.
		agentlessURL := config.AgentlessURL

		if agentlessURL == "" {
			// Use the standard agentless URL format.
			baseURL = fmt.Sprintf("https://%s.%s", subdomain, config.Site)
		} else {
			// Use the custom agentless URL.
			baseURL = agentlessURL
		}

		requestHandler = NewRequestHandler()
	} else {
		// Use agent mode with the EVP proxy.
		defaultHeaders["X-Datadog-EVP-Subdomain"] = subdomain

		agentURL, agentConfigEvents = internalconfig.PrepareAgentURL()
		if agentURL.Scheme == "unix" {
			// If we're connecting over UDS we can just rely on the agent to provide the hostname
			log.Debug("connecting to agent over unix, do not set hostname on any traces")
			requestHandler = NewRequestHandlerWithClient(internal.UDSClient(agentURL.Path, 10*time.Second))
			agentURL = internal.UnixDataSocketURL(agentURL.Path)
		} else {
			requestHandler = NewRequestHandler()
		}

		baseURL = agentURL.String()
	}

	// create random id (the backend associate all transactions with the client request)
	id := strconv.FormatUint(rand.Uint64()&math.MaxInt64, 10)
	defaultHeaders["trace_id"] = id
	defaultHeaders["parent_id"] = id

	log.Debug("ciVisibilityHttpClient: new client created [id: %s, agentless: %t, url: %s, env: %s, serviceName: %s, subdomain: %s]",
		id, agentlessEnabled, baseURL, environment, serviceName, subdomain)

	if !telemetry.Disabled() {
		publishTelemetry := false
		telemetryInit.Do(func() {
			publishTelemetry = true
			telemetryPreparedConfig = []telemetry.Configuration{
				{Name: "service", Value: serviceName},
				{Name: "env", Value: environment},
				{Name: "agentless", Value: agentlessEnabled},
				{Name: "test_session_name", Value: ciTags[constants.TestSessionName]},
			}
			if telemetryGlobalClient() != nil {
				return
			}
			cfg := telemetry.ClientConfig{
				HTTPClient: requestHandler.Client,
				APIKey:     apiKeyValue,
			}
			if agentURL != nil {
				cfg.AgentURL = agentURL.String()
			}
			telemetryPreparedEvents = append(telemetryPreparedEvents, internalconfig.PrepareTelemetryClient(&cfg)...)
			version, versionEvents := internalconfig.PrepareCIVisibilityTelemetryVersion()
			telemetryPreparedEvents = append(telemetryPreparedEvents, versionEvents...)
			client, err := telemetry.NewClient(serviceName, environment, version, cfg)
			if err != nil {
				log.Debug("civisibility: failed to create telemetry client: %s", err.Error())
				return
			}
			telemetryPreparedClient = client
		})
		if publishTelemetry {
			reportTelemetry = func() {
				telemetry.ProductStarted(telemetry.NamespaceCIVisibility)
				telemetry.RegisterAppConfigs(telemetryPreparedConfig...)
				if telemetryPreparedClient != nil {
					telemetry.StartApp(telemetryPreparedClient)
				}
				internalconfig.ReportCIVisibilityConfigEvents(telemetryPreparedEvents)
			}
		}
	}

	// we try to get the branch name
	bName := ciTags[constants.GitBranch]
	if bName == "" {
		// if not we try to use the tag (checkout over a tag)
		bName = ciTags[constants.GitTag]
	}
	if bName == "" {
		// if is still empty we assume the customer just used a detached HEAD
		bName = "auto:git-detached-head"
	}

	return &client{
		id:                id,
		agentless:         agentlessEnabled,
		baseURL:           baseURL,
		environment:       environment,
		serviceName:       serviceName,
		workingDirectory:  ciTags[constants.CIWorkspacePath],
		repositoryURL:     ciTags[constants.GitRepositoryURL],
		commitSha:         ciTags[constants.GitCommitSHA],
		commitMessage:     ciTags[constants.GitCommitMessage],
		headCommitSha:     ciTags[constants.GitHeadCommit],
		headCommitMessage: ciTags[constants.GitHeadMessage],
		branchName:        bName,
		testConfigurations: testConfigurations{
			OsPlatform:     ciTags[constants.OSPlatform],
			OsVersion:      ciTags[constants.OSVersion],
			OsArchitecture: ciTags[constants.OSArchitecture],
			RuntimeName:    ciTags[constants.RuntimeName],
			RuntimeVersion: ciTags[constants.RuntimeVersion],
			Custom:         customConfiguration,
		},
		readCacheScopeIdentity: newReadCacheScopeIdentity(ciTags),
		headers:                defaultHeaders,
		handler:                requestHandler,
	}, report
}

// NewClientWithServiceName creates a new client with the given service name.
func NewClientWithServiceName(serviceName string) Client {
	return NewClientWithServiceNameAndSubdomain(serviceName, "api")
}

// PrepareClientWithServiceName constructs a CI Visibility API client without
// publishing configuration callbacks.
func PrepareClientWithServiceName(serviceName string) (Client, func()) {
	return PrepareClientWithServiceNameAndSubdomain(serviceName, "api")
}

// NewClient creates a new client with the default service name.
func NewClient() Client {
	return NewClientWithServiceName("")
}

// CloseIdleConnections closes idle HTTP connections owned by this CI
// Visibility client.
func (c *client) CloseIdleConnections() {
	if c == nil || c.handler == nil {
		return
	}
	c.handler.CloseIdleConnections()
}

// getURLPath returns the full URL path for the given URL path.
func (c *client) getURLPath(urlPath string) string {
	if c.agentless {
		return fmt.Sprintf("%s/%s", c.baseURL, urlPath)
	}

	return fmt.Sprintf("%s/%s/%s", c.baseURL, "evp_proxy/v2", urlPath)
}

// getPostRequestConfig	returns a new RequestConfig for a POST request.
func (c *client) getPostRequestConfig(url string, body any) *RequestConfig {
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

// SetCoverageFlags sets the coverage report flags to the client
func (c *client) SetCoverageFlags(flags []string) {
	c.coverageReportFlags = flags
}
