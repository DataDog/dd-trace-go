// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"os"
	"sync"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"
)

// Enabled returns true when AppSec is up and running. Meaning that the appsec build tag is enabled, the env var
// DD_APPSEC_ENABLED is set to true, and the tracer is started.
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return activeAppSec != nil && activeAppSec.started
}

// Start AppSec when enabled is enabled by both using the appsec build tag and
// setting the environment variable DD_APPSEC_ENABLED to true.
func Start(opts ...StartOption) {
	enabled, err := isEnabled()
	if err != nil {
		logUnexpectedStartError(err)
		return
	}

	cfg, err := newConfig()
	if err != nil {
		logUnexpectedStartError(err)
		return
	}
	for _, opt := range opts {
		opt(cfg)
	}
	appsec := newAppSec(cfg)

	if !enabled {
		// Check if the env var is set. If it is, appsec is specifically disabled. If not, it is disabled but can be
		// enabled through remote config, and we should start the rc client
		if _, set := os.LookupEnv(enabledEnvVar); set {
			log.Debug("appsec: disabled by the configuration: set the environment variable DD_APPSEC_ENABLED to true to enable it")
			return
		}
		log.Debug("appsec: %s is not set. AppSec won't start until activated through remote configuration", enabledEnvVar)
	} else { // AppSec is specifically enabled
		if err := appsec.start(); err != nil {
			logUnexpectedStartError(err)
			return
		}
	}
	if appsec.rc != nil {
		//TODO: use callbacks to process product updates using rc.RegisterCallback()
		appsec.rc.RegisterCallback(func(update *rc.Update) {
			log.Debug("UPDATE FEATURES PRODUCT MASHALLA GAMING")
		}, rc.ProductFeatures)
		go appsec.rc.Start()
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
	mu           sync.RWMutex
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
	limiter       *TokenTicker
	rc            *remoteconfig.Client
	started       bool
}

// NewAppSec instantiates an appsec object that can be manipulated through the AppSec interface.
func newAppSec(cfg *Config) *appsec {
	// Declare RC capabilities for AppSec
	cfg.rc.Capabilities = append(cfg.rc.Capabilities, remoteconfig.ASMActivation)
	rc, err := remoteconfig.NewClient(cfg.rc)
	if err != nil {
		log.Warn("Could not create remote configuration client. Feature will be disabled.")
	}
	return &appsec{
		cfg: cfg,
		rc:  rc,
	}
}

// Start AppSec by registering its security protections according to the configured the security rules.
func (a *appsec) start() error {
	a.limiter = NewTokenTicker(int64(a.cfg.traceRateLimit), int64(a.cfg.traceRateLimit))
	a.limiter.Start()
	// Register the WAF operation event listener
	unregisterWAF, err := registerWAF(a.cfg.rules, a.cfg.wafTimeout, a.limiter, &a.cfg.obfuscator)
	if err != nil {
		return err
	}
	a.unregisterWAF = unregisterWAF
	a.started = true
	return nil
}

// Stop AppSec by unregistering the security protections.
func (a *appsec) stop() {
	a.started = false
	a.unregisterWAF()
	a.limiter.Stop()
	a.rc.Stop()
}
