// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package subtesthelper

import "testing"

func Disabled(*testing.T) {
	panic("disabled production-helper callback ran")
}

func Pass(*testing.T) {}

// Root returns a callback whose module and source belong to this helper.
func Root(instrument func(func(*testing.T)) func(*testing.T), after func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		t.Run("disabled", instrument(Disabled))
		t.Run("child", instrument(Pass))
		after(t)
	}
}

// TestifySuite exercises suites defined outside the consuming test package.
type TestifySuite struct {
	T      *testing.T
	OnTest func(*testing.T)
}

func (s *TestifySuite) TestSource() {
	s.OnTest(s.T)
}
