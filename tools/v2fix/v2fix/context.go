// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

type contextKey string

const (
	argsKey         contextKey = "args"
	declaredTypeKey contextKey = "declared_type"
	endKey          contextKey = "end"
	fnKey           contextKey = "fn"
	pkgNameKey      contextKey = "pkg_name"
	pkgPrefixKey    contextKey = "pkg_prefix"
	pkgPathKey      contextKey = "pkg_path"
	posKey          contextKey = "pos"
	typeKey         contextKey = "type"
	callExprKey     contextKey = "call_expr"
)
