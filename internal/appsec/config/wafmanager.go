// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"runtime"
	"sync"

	"github.com/DataDog/appsec-internal-go/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"github.com/DataDog/go-libddwaf/v4"
)

type (
	// WAFManager holds a [libddwaf.Builder] and allows managing its configuration.
	WAFManager struct {
		builder       *libddwaf.Builder
		initRules     any
		rulesVersion  string
		defaultLoaded bool
		closed        bool
		// mu is used to guard access to the [WAFManager] state.
		mu sync.RWMutex
	}

	// WAFManagerUpdater is a helper obtained from [WAFManager.LockForUpdates] that is used to perform
	// changes to the current configuration loaded in the [WAFManager]. The [WAFManagerUpdater.Unlock]
	// function must be called once done.
	WAFManagerUpdater struct {
		manager *WAFManager
	}
)

const defaultRulesPath = "ASM_DD/default"

// NewWAFManager creates a new [WAFManager] with the provided [appsec.ObfuscatorConfig] and initial
// rules (if any).
func NewWAFManager(obfuscator appsec.ObfuscatorConfig, defaultRules any) (*WAFManager, error) {
	builder, err := libddwaf.NewBuilder(obfuscator.KeyRegex, obfuscator.ValueRegex)
	if err != nil {
		return nil, err
	}

	rulesVersion := ""
	if defaultRules != nil {
		diag, err := builder.AddOrUpdateConfig(defaultRulesPath, defaultRules)
		if err != nil {
			builder.Close()
			return nil, err
		}
		diag.EachFeature(func(name string, feature *libddwaf.Feature) {
			if feature.Error != "" {
				log.Error("%s", feature.Error, telemetry.WithTags([]string{"appsec_config_key:" + name, "log_type:local::diagnostic"}))
			}
			for msg, ids := range feature.Errors {
				log.Error("%s: %q", msg, ids, telemetry.WithTags([]string{"appsec_config_key:" + name, "log_type:local::diagnostic"}))
			}
			for msg, ids := range feature.Warnings {
				log.Warn("%s: %q", msg, ids, telemetry.WithTags([]string{"appsec_config_key:" + name, "log_type:local::diagnostic"}))
			}
		})
		rulesVersion = diag.Version
	}

	mgr := &WAFManager{
		builder:       builder,
		initRules:     defaultRules,
		rulesVersion:  rulesVersion,
		defaultLoaded: defaultRules != nil,
	}
	// Attach a finalizer to close the builder when it is garbage collected, in case
	// [WAFManager.Close] is not called explicitly by the user. The call to [libddwaf.Builder.Close]
	// is safe to make multiple times.
	runtime.SetFinalizer(mgr, func(m *WAFManager) { m.doClose(true) })

	return mgr, nil
}

// LockForUpdates returns a [WAFManagerUpdater] that can be used to perform multiple updates to the
// receiving [WAFManager] while preventing [WAFManager.NewHandle] from creating new handles with
// partially updated configurations. The caller must call [WAFManagerUpdater.Unlock] once done. The
// goroutine that called [WAFManager.LockForUpdates] must be careful not to call other methods on
// the receiving [WAFManager] until [WAFManagerUpdater.Unlock] has been called, as these will result
// in a deadlock.
func (m *WAFManager) LockForUpdates() *WAFManagerUpdater {
	m.mu.Lock()
	return &WAFManagerUpdater{manager: m}
}

// Reset resets the WAF manager to its initial state.
func (m *WAFManager) Reset() error {
	bulk := m.LockForUpdates()
	for _, path := range m.builder.ConfigPaths("") {
		bulk.RemoveConfig(path)
	}
	err := bulk.RestoreDefaultConfig()
	bulk.Unlock()
	return err
}

// ConfigPaths returns the list of configuration paths currently loaded in the receiving
// [WAFManager]. This is typically used for testing purposes.
func (m *WAFManager) ConfigPaths() []string {
	m.mu.RLock()
	res := m.builder.ConfigPaths("")
	m.mu.RUnlock()
	return res
}

// NewHandle returns a new [*libddwaf.Handle] (which may be nil if no valid WAF could be built) and the
// version of the rules that were used to build it.
func (m *WAFManager) NewHandle() (*libddwaf.Handle, string) {
	m.mu.RLock()
	rulesVersion := m.rulesVersion
	hdl := m.builder.Build()
	m.mu.RUnlock()
	return hdl, rulesVersion
}

// Close releases all resources associated with this [WAFManager].
func (m *WAFManager) Close() {
	m.doClose(false)
}

func (m *WAFManager) doClose(leaked bool) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if leaked {
		log.Warn("WAFManager was leaked and is being closed by GC. Remember to call WAFManager.Close() explicitly!")
	}

	m.builder.Close()
	m.rulesVersion = ""
	m.closed = true
	m.mu.Unlock()
}

// RemoveConfig removes a configuration from the receiving [WAFManager].
func (m *WAFManagerUpdater) RemoveConfig(path string) {
	m.manager.builder.RemoveConfig(path)
}

// RemoveDefaultConfig removes the initial configuration from the receiving [WAFManager]. Returns
// true if the default config was actually removed; false otherwise (e.g, if it had previously been
// removed, or there was no default config to begin with).
func (m *WAFManagerUpdater) RemoveDefaultConfig() bool {
	removed := false
	if m.manager.defaultLoaded {
		removed = m.manager.builder.RemoveConfig(defaultRulesPath)
		m.manager.defaultLoaded = false
	}
	return removed
}

// AddOrUpdateConfig adds or updates a configuration in the receiving [WAFManager].
func (m *WAFManagerUpdater) AddOrUpdateConfig(path string, fragment any) (libddwaf.Diagnostics, error) {
	diag, err := m.manager.builder.AddOrUpdateConfig(path, fragment)
	if err == nil && diag.Version != "" {
		m.manager.rulesVersion = diag.Version
	}

	// Submit the telemetry metrics for error counts obtained from the [libddwaf.Diagnostics] object.
	// See: https://docs.google.com/document/d/1lcCvURsWTS_p01-MvrI6SmDB309L1e8bx9txuUR1zCk/edit?tab=t.0#heading=h.nwzm8andnx41
	eventRulesVersion := diag.Version
	if eventRulesVersion == "" {
		eventRulesVersion = m.manager.rulesVersion
	}
	diag.EachFeature(func(name string, feat *libddwaf.Feature) {
		errCount := telemetry.Count(telemetry.NamespaceAppSec, "waf.config_errors", []string{
			"waf_version:" + libddwaf.Version(),
			"event_rules_version:" + eventRulesVersion,
			"config_key:" + name,
			"scope:item",
			"action:update",
		})
		errCount.Submit(0)
		for _, ids := range feat.Errors {
			errCount.Submit(float64(len(ids)))
		}
	})

	return diag, err
}

// RestoreDefaultConfig restores the initial configurations to the receiving [WAFManager].
func (m *WAFManagerUpdater) RestoreDefaultConfig() error {
	if m.manager.initRules == nil {
		return nil
	}
	_, err := m.AddOrUpdateConfig(defaultRulesPath, m.manager.initRules)
	return err
}

// Unlock releases the lock on the [WAFManager]. The receiving [WAFManagerUpdater] must not be
// used after calling this method.
func (m *WAFManagerUpdater) Unlock() {
	m.manager.mu.Unlock()
	m.manager = nil
}
