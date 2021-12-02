// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"io/ioutil"
	"os"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Default batching configuration values.
const (
	defaultMaxBatchLen       = 1024
	defaultMaxBatchStaleTime = time.Second
)

// Status returns the AppSec status string: "enabled" when both the appsec
// build tag is enabled and the env var DD_APPSEC_ENABLED is set to true, or
// "disabled" otherwise.
func Status() string {
	if enabled, _ := isEnabled(); enabled {
		return "enabled"
	}
	return "disabled"
}

// Start AppSec when enabled is enabled by both using the appsec build tag and
// setting the environment variable DD_APPSEC_ENABLED to true.
func Start() {
	cfg := &Config{}
	enabled, err := isEnabled()
	if err != nil {
		logUnexpectedStartError(err)
		return
	}
	if !enabled {
		log.Debug("appsec: disabled by the configuration: set the environment variable DD_APPSEC_ENABLED to true to enable it")
		return
	}

	filepath := os.Getenv("DD_APPSEC_RULES")
	if filepath != "" {
		rules, err := ioutil.ReadFile(filepath)
		if err != nil {
			if os.IsNotExist(err) {
				log.Error("appsec: could not find the rules file in path %s: %v.\nAppSec will not run any protections in this application. No security activities will be collected.", filepath, err)
			} else {
				logUnexpectedStartError(err)
			}
			return
		}
		cfg.rules = rules
		log.Info("appsec: starting with the security rules from file %s", filepath)
	} else {
		log.Info("appsec: starting with default recommended security rules")
	}

	appsec := newAppSec(cfg)
	if err := appsec.start(); err != nil {
		logUnexpectedStartError(err)
		return
	}
	setActiveAppSec(appsec)
}

// Implement the AppSec log message C1
func logUnexpectedStartError(err error) {
	log.Error("appsec: could not start because of an unexpected error: %v\nNo security activities will be collected. Please contact support at https://docs.datadoghq.com/help/ for help.", err)
}

// Stop AppSec.
func Stop() {
	setActiveAppSec(nil)
}

var (
	activeAppSec *appsec
	mu           sync.Mutex
)

func setActiveAppSec(a *appsec) {
	mu.Lock()
	defer mu.Unlock()
	if activeAppSec != nil {
		activeAppSec.stop()
	}
	activeAppSec = a
}

type appsec struct {
	cfg           *Config
	unregisterWAF dyngo.UnregisterFunc
}

func newAppSec(cfg *Config) *appsec {
	if cfg.MaxBatchLen <= 0 {
		cfg.MaxBatchLen = defaultMaxBatchLen
	}
	if cfg.MaxBatchStaleTime <= 0 {
		cfg.MaxBatchStaleTime = defaultMaxBatchStaleTime
	}
	return &appsec{
		cfg: cfg,
	}
}

// Start AppSec by registering its security protections according to the configured the security rules.
func (a *appsec) start() error {
	// Register the WAF operation event listener
	unregisterWAF, err := registerWAF(a.cfg.rules, a)
	if err != nil {
		return err
	}
	a.unregisterWAF = unregisterWAF
	return nil
}

// Stop AppSec by unregistering the security protections.
func (a *appsec) stop() {
	a.unregisterWAF()
}
