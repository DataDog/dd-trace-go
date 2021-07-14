// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package globalconfig stores configuration which applies globally to both the tracer
// and integrations.
package globalconfig

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/bitset"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/google/uuid"
)

var cfg = &config{
	analyticsRate:   math.NaN(),
	runtimeID:       uuid.New().String(),
	httpClientCodes: parseHTTPCodeRanges("400-499"),
	httpServerCodes: parseHTTPCodeRanges("500-599"),
}

type config struct {
	mu            sync.RWMutex
	analyticsRate float64
	serviceName   string
	runtimeID     string

	// specifies the range of HTTP client/server status codes considered as errors.
	httpClientCodes *bitset.BitSet
	httpServerCodes *bitset.BitSet
}

// AnalyticsRate returns the sampling rate at which events should be marked. It uses
// synchronizing mechanisms, meaning that for optimal performance it's best to read it
// once and store it.
func AnalyticsRate() float64 {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cfg.analyticsRate
}

// SetAnalyticsRate sets the given event sampling rate globally.
func SetAnalyticsRate(rate float64) {
	cfg.mu.Lock()
	cfg.analyticsRate = rate
	cfg.mu.Unlock()
}

// ServiceName returns the default service name used by non-client integrations such as servers and frameworks.
func ServiceName() string {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cfg.serviceName
}

// SetServiceName sets the global service name set for this application.
func SetServiceName(name string) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	cfg.serviceName = name
}

// RuntimeID returns this process's unique runtime id.
func RuntimeID() string {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cfg.runtimeID
}

// HTTPClientCodes returns the http client codes identified as errors.
func HTTPClientCodes() *bitset.BitSet {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cfg.httpClientCodes
}

// SetHTTPClientCodes sets the http client codes identified as errors.
func SetHTTPClientCodes(codes string) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	cfg.httpClientCodes = parseHTTPCodeRanges(codes)
}

// HTTPServerCodes returns the http server codes identified as errors.
func HTTPServerCodes() *bitset.BitSet {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cfg.httpServerCodes
}

// SetHTTPServerCodes sets the http server codes identified as errors.
func SetHTTPServerCodes(codes string) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	cfg.httpServerCodes = parseHTTPCodeRanges(codes)
}

// IsHTTPClientError checks if the bitset of HTTP client codes contains a given HTTP client error code.
func IsHTTPClientError(c int) bool {
	return HTTPClientCodes().Contains(uint(c))
}

// IsHTTPServerError checks if the bitset of HTTP server codes contains a given HTTP server error code.
func IsHTTPServerError(c int) bool {
	return HTTPServerCodes().Contains(uint(c))
}

// parseHTTPCodeRanges parses range pairs and returns a bitset mapping of HTTP status codes.
func parseHTTPCodeRanges(r string) *bitset.BitSet {
	re := regexp.MustCompile("\\d{3}(?:-\\d{3})*(?:,\\d{3}(?:-\\d{3})*)*")
	codes := bitset.New(0)
	for _, code := range strings.Split(r, ",") {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if !re.MatchString(code) {
			log.Warn("Invalid range for %v", code)
			continue
		}
		rg := strings.Split(code, "-")
		if len(rg) == 1 {
			val, _ := strconv.Atoi(rg[0])
			codes.Add(uint(val))
		} else {
			if rg[0] > rg[1] {
				rg[0], rg[1] = rg[1], rg[0]
			}
			min, err := strconv.Atoi(rg[0])
			if err != nil {
				log.Warn("Invalid input.")
			}
			max, err := strconv.Atoi(rg[1])
			if err != nil {
				log.Warn("Invalid input.")
			}
			for i := min; i <= max; i++ {
				codes.Add(uint(i))
			}
		}
	}
	return codes
}
