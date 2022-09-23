// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"os"
	"sync/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

// AppSec is an interface allowing to use an appsec object for application security
type AppSec interface {
	Start()
	Stop()
}

// instances tracks how many appsec object are currently instantiated and running
var instances atomic.Uint32

// Enabled returns true when AppSec is up and running. Meaning that the appsec build tag is enabled, the env var
// DD_APPSEC_ENABLED is set to true, and the tracer is started.
func Enabled() bool {
	return instances.Load() > 0
}

// Start AppSec when enabled is enabled by both using the appsec build tag and
// setting the environment variable DD_APPSEC_ENABLED to true.
func (a *appsec) Start() {
	enabled, err := isEnabled()
	if err != nil {
		logUnexpectedStartError(err)
		return
	}
	if !enabled {
		// Check if the env var is set. If it is, appsec is specifically disabled. If not, it is disabled but can be
		// enabled through remote config, and we should start the rc client
		if _, set := os.LookupEnv(enabledEnvVar); set {
			log.Debug("appsec: disabled by the configuration: set the environment variable DD_APPSEC_ENABLED to true to enable it")
			return
		}
		log.Debug("appsec: %s is not set. AppSec won't start until activated through remote configuration", enabledEnvVar)
	} else if err := a.start(); err != nil {
		logUnexpectedStartError(err)
		return
	}
	if a.rc != nil {
		a.rc.RegisterCallback(func(u *rc.Update) {
			log.Debug("appsec: FEATURES UPDATE")
		}, rc.ProductFeatures)
		go a.rc.Start()
	}
	instances.Add(1)
}

// Implement the AppSec log message C1
func logUnexpectedStartError(err error) {
	log.Error("appsec: could not start because of an unexpected error: %v\nNo security activities will be collected. Please contact support at https://docs.datadoghq.com/help/ for help.", err)
}

func (a *appsec) Stop() {
	a.stop()
	for i := instances.Load(); !instances.CompareAndSwap(i, i-1); {
	}
	if a.rc != nil {
		a.rc.Stop()
	}
}

type appsec struct {
	cfg           *Config
	unregisterWAF dyngo.UnregisterFunc
	limiter       *TokenTicker
	rc            *remoteconfig.Client
}

// NewAppSec instantiates an appsec object that can be manipulated through the AppSec interface.
func NewAppSec(opts ...StartOption) AppSec {
	cfg, err := newConfig()
	if err != nil {
		log.Error(err.Error())
		return nil
	}
	for _, opt := range opts {
		opt(cfg)
	}
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
	return nil
}

// Stop AppSec by unregistering the security protections.
func (a *appsec) stop() {
	a.unregisterWAF()
	a.limiter.Stop()
}
