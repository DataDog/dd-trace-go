// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"reflect"
	"testing"
)

func TestTestifyLikeTest(t *testing.T) {
	if parallelEfd {
		t.Skip("Skipping TestTestifyLikeTest in parallel mode")
	}
	mySuite := new(MySuite)
	registerTestifySuite(t, mySuite)
	Run(t, mySuite)

	tParent := t
	t.Run("check_suite_registration", func(t *testing.T) {
		if tests, ok := getTestifyTestsByParentT(reflect.ValueOf(tParent).UnsafePointer()); ok {
			if len(tests) != 1 {
				t.Errorf("Expected 1 test to be registered, got %d", len(tests))
			}

			test := tests[0]
			if test.methodName != "TestMySuite" {
				t.Errorf("Expected method name to be TestMySuite, got %s", test.methodName)
			}
			if test.suiteName != "testify_test.go/MySuite" {
				t.Errorf("Expected suite name to be testify_test.go/MySuite, got %s", test.suiteName)
			}
			if test.moduleName != "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting" {
				t.Errorf("Expected module name to be github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting, got %s", test.moduleName)
			}
		} else {
			t.Errorf("Expected the parent T to be registered, got %v", reflect.ValueOf(tParent).UnsafePointer())
		}
	})
}

func (s *MySuite) TestMySuite() {
	t := (*T)(s.T)
	t.Log("This is a test")
	t.Run("sub01", func(_ *testing.T) {
	})
}
