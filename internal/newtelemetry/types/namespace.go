// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package types

// Namespace describes an APM product to distinguish telemetry coming from
// different products used by the same application
type Namespace string

const (
	NamespaceGeneral   Namespace = "general"
	NamespaceTracers   Namespace = "tracers"
	NamespaceProfilers Namespace = "profilers"
	NamespaceAppSec    Namespace = "appsec"
	NamespaceIAST      Namespace = "iast"
	NamespaceTelemetry Namespace = "telemetry"
)
