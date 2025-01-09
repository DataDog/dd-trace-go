// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package civisibility

import "testing"

func TestNormal(t *testing.T) {
	t.Log("Normal test")
}

func TestWithSubTests(t *testing.T) {
	t.Run("Sub1", func(t *testing.T) {
		t.Log("Sub test 1")
	})
	t.Run("Sub2", func(t *testing.T) {
		t.Log("Sub test 2")
	})
}

func TestFail(t *testing.T) {
	t.Fail()
}

func TestError(t *testing.T) {
	t.Error("My error test")
}

func TestErrorf(t *testing.T) {
	t.Errorf("My error test: %s", t.Name())
}

func TestSkip(t *testing.T) {
	t.Skip("My skipped test")
}

func TestSkipf(t *testing.T) {
	t.Skipf("My skipped test: %s", t.Name())
}

func TestSkipNow(t *testing.T) {
	t.SkipNow()
}
