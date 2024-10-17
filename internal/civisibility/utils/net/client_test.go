// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func saveEnv() []string {
	return os.Environ()
}

func restoreEnv(env []string) {
	os.Clearenv()
	for _, e := range env {
		kv := strings.SplitN(e, "=", 2)
		os.Setenv(kv[0], kv[1])
	}
}

func TestNewClient_DefaultValues(t *testing.T) {
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	os.Clearenv()
	os.Setenv("PATH", path)
	// Do not set any environment variables to simulate default behavior

	cInterface := NewClient()
	if cInterface == nil {
		t.Fatal("Expected non-nil client")
	}

	c, ok := cInterface.(*client)
	if !ok {
		t.Fatal("Expected client to be of type *client")
	}

	if c.environment != "none" {
		t.Errorf("Expected environment 'none', got '%s'", c.environment)
	}

	if c.agentless {
		t.Errorf("Expected agentless to be false")
	}

	// Since serviceName depends on CI tags, which we cannot mock without access to internal functions,
	// we check if serviceName is set or not empty.
	if c.serviceName == "" {
		t.Errorf("Expected serviceName to be set, got empty string")
	}
}

func TestNewClient_AgentlessEnabled(t *testing.T) {
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	os.Clearenv()
	os.Setenv("PATH", path)
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	os.Setenv("DD_API_KEY", "test_api_key")
	os.Setenv("DD_SITE", "site.com")

	cInterface := NewClient()
	if cInterface == nil {
		t.Fatal("Expected non-nil client")
	}

	c, ok := cInterface.(*client)
	if !ok {
		t.Fatal("Expected client to be of type *client")
	}

	if !c.agentless {
		t.Errorf("Expected agentless to be true")
	}

	expectedBaseURL := "https://api.site.com"
	if c.baseURL != expectedBaseURL {
		t.Errorf("Expected baseUrl '%s', got '%s'", expectedBaseURL, c.baseURL)
	}

	if c.headers["dd-api-key"] != "test_api_key" {
		t.Errorf("Expected dd-api-key 'test_api_key', got '%s'", c.headers["dd-api-key"])
	}
}

func TestNewClient_AgentlessEnabledWithNoApiKey(t *testing.T) {
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	os.Clearenv()
	os.Setenv("PATH", path)
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")

	cInterface := NewClient()
	if cInterface != nil {
		t.Fatal("Expected nil client")
	}
}

func TestNewClient_CustomAgentlessURL(t *testing.T) {
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, "https://custom.agentless.url")

	cInterface := NewClient()
	if cInterface == nil {
		t.Fatal("Expected non-nil client")
	}

	c, ok := cInterface.(*client)
	if !ok {
		t.Fatal("Expected client to be of type *client")
	}

	if c.baseURL != "https://custom.agentless.url" {
		t.Errorf("Expected baseUrl 'https://custom.agentless.url', got '%s'", c.baseURL)
	}
}

func TestClient_getUrlPath_Agentless(t *testing.T) {
	c := &client{
		agentless: true,
		baseURL:   "https://api.customhost.com",
	}

	urlPath := c.getURLPath("some/path")
	expected := "https://api.customhost.com/some/path"
	if urlPath != expected {
		t.Errorf("Expected urlPath '%s', got '%s'", expected, urlPath)
	}
}

func TestClient_getUrlPath_Agent(t *testing.T) {
	c := &client{
		agentless: false,
		baseURL:   "http://agent.url",
	}

	urlPath := c.getURLPath("some/path")
	expected := "http://agent.url/evp_proxy/v2/some/path"
	if urlPath != expected {
		t.Errorf("Expected urlPath '%s', got '%s'", expected, urlPath)
	}
}

func TestClient_getPostRequestConfig(t *testing.T) {
	c := &client{
		agentless: false,
		baseURL:   "http://agent.url",
		headers: map[string]string{
			"trace_id":  "12345",
			"parent_id": "12345",
		},
	}

	body := map[string]string{"key": "value"}
	config := c.getPostRequestConfig("some/path", body)

	if config.Method != "POST" {
		t.Errorf("Expected Method 'POST', got '%s'", config.Method)
	}

	expectedURL := "http://agent.url/evp_proxy/v2/some/path"
	if config.URL != expectedURL {
		t.Errorf("Expected URL '%s', got '%s'", expectedURL, config.URL)
	}

	if !reflect.DeepEqual(config.Headers, c.headers) {
		t.Errorf("Headers do not match")
	}

	if config.Format != FormatJSON {
		t.Errorf("Expected Format 'FormatJSON', got '%v'", config.Format)
	}

	if config.Compressed {
		t.Errorf("Expected Compressed to be false")
	}

	if config.MaxRetries != DefaultMaxRetries {
		t.Errorf("Expected MaxRetries '%d', got '%d'", DefaultMaxRetries, config.MaxRetries)
	}

	if config.Backoff != DefaultBackoff {
		t.Errorf("Expected Backoff '%v', got '%v'", DefaultBackoff, config.Backoff)
	}
}

func TestNewClient_TestConfigurations(t *testing.T) {
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, "https://custom.agentless.url")
	os.Setenv("DD_TAGS", "test.configuration.MyTag:MyValue")

	cInterface := NewClient()
	if cInterface == nil {
		t.Fatal("Expected non-nil client")
	}

	c, ok := cInterface.(*client)
	if !ok {
		t.Fatal("Expected client to be of type *client")
	}

	if c.testConfigurations.Custom["MyTag"] != "MyValue" {
		t.Errorf("Expected 'MyValue', got '%s'", c.testConfigurations.Custom["MyTag"])
	}
}

func setCiVisibilityEnv(path string, url string) {
	os.Clearenv()
	os.Setenv("PATH", path)
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	os.Setenv("DD_API_KEY", "test_api_key")
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", url)
}
