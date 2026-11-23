// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package listener

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
)

// Feature names are stable slugs identifying each AppSec feature. They are returned by [Feature.String] and are the
// values accepted by the DD_APPSEC_DISABLE_FEATURES environment variable.
const (
	FeatureTrace   = "trace"
	FeatureWAF     = "waf"
	FeatureHTTP    = "http"
	FeatureGRPC    = "grpc"
	FeatureGraphQL = "graphql"
	FeatureUser    = "user"
	FeatureSQLi    = "sqli"
	FeatureLFI     = "lfi"
	FeatureCMDi    = "cmdi"
	FeatureSSRF    = "ssrf"
)

// Feature is an interface that represents a feature that can be started and stopped.
type Feature interface {
	// String returns the feature name, one of the Feature* slug constants.
	String() string
	// Stop stops the feature.
	Stop()
}

// NewFeature is a function that creates a new feature.
// The error returned will be fatal for the application if not nil.
// If both the feature and the error are nil, the feature will be considered inactive.
type NewFeature func(*config.Config, dyngo.Operation) (Feature, error)
