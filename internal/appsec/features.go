// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package appsec

import (
	"errors"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/graphqlsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/grpcsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/ossec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/sqlsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/usersec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// featureBuilder associates a feature constructor with the name of the feature it builds.
type featureBuilder struct {
	name string
	new  listener.NewFeature
	// required features are needed by every other feature and cannot be disabled.
	required bool
	// addresses are removed from the WAF supported addresses when the feature is disabled. They are only needed for
	// features whose data can reach the WAF without going through their listener, such as HTTP bodies, HTTP route
	// parameters and gRPC messages.
	addresses []string
}

var features = []featureBuilder{
	{name: listener.FeatureTrace, new: trace.NewAppsecSpanTransport, required: true},
	{name: listener.FeatureWAF, new: waf.NewWAFFeature, required: true},
	{name: listener.FeatureHTTP, new: httpsec.NewHTTPSecFeature, addresses: httpsec.Addresses},
	{name: listener.FeatureGRPC, new: grpcsec.NewGRPCSecFeature, addresses: grpcsec.Addresses},
	{name: listener.FeatureGraphQL, new: graphqlsec.NewGraphQLSecFeature},
	{name: listener.FeatureUser, new: usersec.NewUserSecFeature},
	{name: listener.FeatureSQLi, new: sqlsec.NewSQLSecFeature},
	{name: listener.FeatureLFI, new: ossec.NewOSSecFeature},
	{name: listener.FeatureCMDi, new: ossec.NewExecSecFeature},
	{name: listener.FeatureSSRF, new: httpsec.NewSSRFProtectionFeature},
}

// enabledFeatures returns the constructors of the features not disabled through [config.EnvDisableFeatures], along
// with the WAF addresses of the disabled features. Names that do not match any feature, or that match a required
// feature, are logged and ignored.
func enabledFeatures(disabled map[string]struct{}) (enabled []listener.NewFeature, disabledAddrs config.AddressSet) {
	enabled = make([]listener.NewFeature, 0, len(features))
	disabledAddrs = make(config.AddressSet)
	known := make(map[string]struct{}, len(features))
	for _, f := range features {
		known[f.name] = struct{}{}
		if _, ok := disabled[f.name]; !ok {
			enabled = append(enabled, f.new)
			continue
		}
		if f.required {
			log.Warn("appsec: %s: feature %q cannot be disabled", config.EnvDisableFeatures, f.name)
			enabled = append(enabled, f.new)
			continue
		}
		log.Info("appsec: feature %q disabled by %s", f.name, config.EnvDisableFeatures)
		for _, addr := range f.addresses {
			disabledAddrs[addr] = struct{}{}
		}
	}
	for name := range disabled {
		if _, ok := known[name]; !ok {
			log.Warn("appsec: %s: ignoring unknown feature %q", config.EnvDisableFeatures, name)
		}
	}
	return enabled, disabledAddrs
}

func (a *appsec) SwapRootOperation() error {
	newRoot := dyngo.NewRootOperation()
	newFeatures := make([]listener.Feature, 0, len(a.featureBuilders))
	var featureErrors []error
	for _, newFeature := range a.featureBuilders {
		feature, err := newFeature(a.cfg, newRoot)
		if err != nil {
			featureErrors = append(featureErrors, err)
			continue
		}

		// If error is nil and feature is nil, it means the feature did not activate itself
		if feature == nil {
			continue
		}

		newFeatures = append(newFeatures, feature)
	}

	err := errors.Join(featureErrors...)
	if err != nil {
		for _, feature := range newFeatures {
			feature.Stop()
		}
		return err
	}

	a.featuresMu.Lock()
	defer a.featuresMu.Unlock()

	oldFeatures := a.features
	a.features = newFeatures

	if len(oldFeatures) > 0 {
		log.Debug("appsec: stopping the following features: %q", oldFeatures)
	}
	if len(newFeatures) > 0 {
		log.Debug("appsec: starting the following features: %q", newFeatures)
	}

	dyngo.SwapRootOperation(newRoot)

	log.Debug("appsec: swapped root operation")

	for _, oldFeature := range oldFeatures {
		oldFeature.Stop()
	}

	return nil
}
