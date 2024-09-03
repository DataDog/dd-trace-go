// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package appsec

import (
	"errors"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/ossec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/usersec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/waf"
)

type StopFeature func()

type NewFeature func(*config.Config, dyngo.Operation) (func(), error)

var features = map[string]NewFeature{
	"APM Span Transport":       trace.NewAppsecSpanTransport,
	"Web Application Firewall": waf.NewWAFFeature,
	"User Security":            usersec.NewUserSecFeature,
	"SQL Injection":            sqlsec.NewSQLSecFeature,
	"Local File Inclusion":     ossec.NewOSSecFeature,
	"HTTP Protection":          httpsec.NewHTTPSecFeature,
}

func (a *appsec) SwapRootOperation() error {
	newRoot := dyngo.NewRootOperation()
	newFeatures := make([]StopFeature, 0, len(features))
	var featureErrors []error
	for name, newFeature := range features {
		feature, err := newFeature(a.cfg, newRoot)
		if err != nil {
			featureErrors = append(featureErrors, fmt.Errorf("error creating %q listeners: %w", name, err))
			continue
		}

		newFeatures = append(newFeatures, feature)
	}

	a.featuresMu.Lock()
	defer a.featuresMu.Unlock()

	oldFeatures := a.features
	a.features = newFeatures

	dyngo.SwapRootOperation(newRoot)

	for _, stopper := range oldFeatures {
		stopper()
	}

	return errors.Join(featureErrors...)
}
