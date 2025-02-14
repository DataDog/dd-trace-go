// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package moduleB

import (
	"example.com/root"
	"example.com/root/moduleA"
)

var version = root.Version

// HelloB returns a greeting that includes moduleA's greeting.
func HelloB() string {
	return "Hello B + " + moduleA.HelloA() + " + " + version
}
