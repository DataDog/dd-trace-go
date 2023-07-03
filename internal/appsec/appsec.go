// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"errors"
	"fmt"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"

	waf "github.com/DataDog/go-libddwaf"
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
	// AppSec can start either:
	// 1. Manually thanks to DD_APPSEC_ENABLED
	// 2. Remotely when DD_APPSEC_ENABLED is undefined
	// Note: DD_APPSEC_ENABLED=false takes precedence over remote configuration
	// and enforces to have AppSec disabled.
	enabled, set, err := isEnabled()
	if err != nil {
		logUnexpectedStartError(err)
		return
	}

	// Check if AppSec is explicitly disabled
	if set && !enabled {
		log.Debug("appsec: disabled by the configuration: set the environment variable DD_APPSEC_ENABLED to true to enable it")
		return
	}

	// Check whether libddwaf - required for Threats Detection - can be enabled or not
	if ok, err := waf.Load(); err != nil {
		// Handle the error differently according to the following cases:
		// 1. If the error is about the unsupported target: log as an expected error case and quit appsec
		if actual := (*waf.UnsupportedTargetError)(nil); errors.As(err, &actual) {
			log.Error("appsec: unsupported operating-system or architecture: %v\nNo security activities will be collected. Please contact support at https://docs.datadoghq.com/help/ for help.", err)
			return
		}
		// 2. If there is an error and the loading is not ok: log as an unexpected error case and quit appsec
		if !ok {
			logUnexpectedStartError(fmt.Errorf("error while loading libddwaf: %w", err))
			return
		}
		// 3. If there is an error and the loading is ok: log as an informative error where appsec can be used
		log.Error("appsec: non-critical error while loading libddwaf: %v", err)
	}

	// From this point we know that AppSec is either enabled or can be enabled through remote config
	cfg, err := newConfig()
	if err != nil {
		logUnexpectedStartError(err)
		return
	}
	for _, opt := range opts {
		opt(cfg)
	}
	appsec := newAppSec(cfg)

	// Start the remote configuration client
	log.Debug("appsec: starting the remote configuration client")
	appsec.startRC()

	if !set {
		// AppSec is not enforced by the env var and can be enabled through remote config
		log.Debug("appsec: %s is not set and won't start until activated through remote configuration", enabledEnvVar)
		if err := appsec.enableRemoteActivation(); err != nil {
			// ASM is not enabled and can't be enabled through remote configuration. Nothing more can be done.
			logUnexpectedStartError(err)
			appsec.stopRC()
			return
		}
		log.Debug("appsec: awaiting for possible remote activation")
	} else if err := appsec.start(); err != nil { // AppSec is specifically enabled
		logUnexpectedStartError(err)
		appsec.stopRC()
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
	mu           sync.RWMutex
)

func setActiveAppSec(a *appsec) {
	mu.Lock()
	defer mu.Unlock()
	if activeAppSec != nil {
		activeAppSec.stopRC()
		activeAppSec.stop()
	}
	activeAppSec = a
}

type appsec struct {
	cfg       *Config
	limiter   *TokenTicker
	rc        *remoteconfig.Client
	wafHandle *waf.Handle
	started   bool
}

func newAppSec(cfg *Config) *appsec {
	var client *remoteconfig.Client
	var err error
	if cfg.rc != nil {
		client, err = remoteconfig.NewClient(*cfg.rc)
	}
	if err != nil {
		log.Error("appsec: Remote config: disabled due to a client creation error: %v", err)
	}
	return &appsec{
		cfg: cfg,
		rc:  client,
	}
}

// Start AppSec by registering its security protections according to the configured the security rules.
func (a *appsec) start() error {
	a.limiter = NewTokenTicker(int64(a.cfg.traceRateLimit), int64(a.cfg.traceRateLimit))
	a.limiter.Start()
	// Register the WAF operation event listener
	if err := a.swapWAF(a.cfg.rulesManager.latest); err != nil {
		return err
	}
	a.enableRCBlocking()
	a.started = true
	log.Info("appsec: up and running")
	// TODO: log the config like the APM tracer does but we first need to define
	//   and user-friendly string representation of our config and its sources
	return nil
}

// Stop AppSec by unregistering the security protections.
func (a *appsec) stop() {
	if !a.started {
		return
	}
	a.started = false
	// Disable RC blocking first so that the following is guaranteed not to be concurrent anymore.
	a.disableRCBlocking()

	// Disable the currently applied instrumentation
	dyngo.SwapRootOperation(nil)
	if a.wafHandle != nil {
		a.wafHandle.Close()
	}
	// TODO: block until no more requests are using dyngo operations

	a.limiter.Stop()
}
