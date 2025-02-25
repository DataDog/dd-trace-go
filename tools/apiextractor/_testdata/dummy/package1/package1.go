// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package package1

// PublicFunc is a public function in package1.
func PublicFunc() {
	privateFunc()
	_ = PublicType{
		privateField: "private",
	}
	_ = privateType{
		PublicField: "public",
	}
}

// privateFunc is a private function in package1.
func privateFunc() {}

// PublicType is a public type in package1.
type PublicType struct {
	// PublicField is a public field in PublicType.
	PublicField string
	// privateField is a private field in PublicType.
	privateField string
}

// privateType is a private type in package1.
type privateType struct {
	// PublicField is a public field in privateType.
	PublicField string
}
