// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package package2

// PublicFunc2 is a public function in package2.
func PublicFunc2() {
	privateFunc2()
	_ = PublicType2{
		privateField2: "private",
	}
	_ = privateType2{
		PublicField2: "public",
	}
}

// privateFunc2 is a private function in package2.
func privateFunc2() {}

// PublicType2 is a public type in package2.
type PublicType2 struct {
	// PublicField2 is a public field in PublicType2.
	PublicField2 string
	// privateField2 is a private field in PublicType2.
	privateField2 string
}

// privateType2 is a private type in package2.
type privateType2 struct {
	// PublicField2 is a public field in privateType2.
	PublicField2 string
}
