/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import "time"

const (
	apiKeyParam                        = "api_key"
	defaultRetryInterval               = time.Millisecond * 250
	defaultBatchInterval               = time.Second * 15
	defaultHttpClientTimeout           = time.Second * 5
	defaultCircuitBreakerInterval      = time.Second * 30
	defaultCircuitBreakerTimeout       = time.Second * 60
	defaultCircuitBreakerTotalFailures = 4
)

// MetricType enumerates all the available metric types
type MetricType string

const (

	// DistributionType represents a distribution metric
	DistributionType MetricType = "distribution"
)
