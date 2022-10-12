// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package gqlgen provides functions to trace the 99designs/gqlgen package (https://github.com/99designs/gqlgen).
package gqlgen_test

// The example was moved into the package godoc to avoid adding its dependencies into dd-trace-go's go.mod file,
// where github.com/BurntSushi/toml would be upgraded to v1, which breaks existing users of the v0, like this is the
// case at Datadog's backend. Such code examples shouldn't introduce breaking changes to dd-trace-go's go.mod file.
