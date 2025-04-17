// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"maps"
	"net/http"
	"os"
	"runtime/debug"
	"slices"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

func getTracerVersion() string {
	info, _ := debug.ReadBuildInfo()
	for _, dep := range info.Deps {
		if dep.Path == "github.com/DataDog/dd-trace-go/v2" {
			return dep.Version
		}
	}

	return ""
}

type Config struct {
	ListenAddr      string
	HealthCheckAddr string

	// RequestIDFunc is a function that returns the request ID for the given request, will be used on responses and requests
	RequestIDFunc func(*http.Request) string

	// ServeConfig is the configuration for the httptrace.ServeConfig
	ServeConfig *httptrace.ServeConfig

	// Timeout is used if ResponseAsRequest is set to true, it will be used as a timeout to wait for the response to be sent in another request
	Timeout time.Duration

	// Features list the features supported by the proxy server which mirrored the traffic
	Features
}

type Features struct {
	// Body is set if the proxy support sending us the request (and response if ResponseAsRequest is true) body
	Body bool
	// Blocking is the support for sending a custom response to the proxy and using instead of the backend response
	Blocking bool
	// ResponseAsRequest is the support for receiving the response from the real backend as a request and scanning it
	// setting this means Config.RequestIDFunc will be used to get the request ID for the response and should not be nil
	ResponseAsRequest bool
	// NoResponse is to signify the proxy should not receive any response and terminate the connection
	NoResponse bool
}

func setEnvIfNotFound(key, value string) {
	if _, set := os.LookupEnv(key); !set {
		os.Setenv(key, value)
	}
}

func (cfg Config) SetEnv() {
	for k, v := range defaultEnv {
		setEnvIfNotFound(k, v)
	}

	if !cfg.Features.Blocking {
		os.Setenv("_DD_APPSEC_BLOCKING_UNAVAILABLE", "true")
	}
}

var defaultEnv = map[string]string{
	"DD_APPSEC_ENABLED":     "true",
	"DD_APPSEC_WAF_TIMEOUT": "10ms", // increase the default timeout because we are not in-app
	"DD_VERSION":            getTracerVersion(),
	"DD_SERVICE":            "traffic-mirror",

	// TODO: set once API Sec sampling works with AppSec standalone
	//"DD_APM_TRACING_ENABLED":          "false", // Disable APM tracing by default
}

func getConfig() Config {
	cfg := Config{
		Timeout:         30 * time.Second,
		ListenAddr:      os.Getenv("DD_TRAFFIC_MIRROR_LISTEN_ADDR"),
		HealthCheckAddr: os.Getenv("DD_TRAFFIC_MIRROR_HEALTHCHECK_ADDR"),
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}

	if cfg.HealthCheckAddr == "" {
		cfg.HealthCheckAddr = ":8081"
	}

	profileName := os.Getenv("DD_TRAFFIC_MIRROR_PROFILE")
	if profileName == "" {
		profileName = "default"
	}

	profileFunc, ok := profiles[profileName]
	if !ok {
		profileNames := slices.Collect(maps.Keys(profiles))
		instr.Logger().Error("Unknown profile name %q. Available options are %v", profileName, profileNames)
	}

	profileFunc(&cfg)

	instr.Logger().Info("Applied traffic mirroring profile %q", profileName)

	return cfg
}

var profiles = map[string]func(*Config){
	"default": func(cfg *Config) {
		cfg.Features = Features{
			Body:              true,
			Blocking:          false,
			ResponseAsRequest: false,
			NoResponse:        true,
		}
	},
}
