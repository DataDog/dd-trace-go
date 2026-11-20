// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package events

import (
	"errors"
	"testing"
)

func TestBlockingSecurityEventError(t *testing.T) {
	err := (&BlockingSecurityEvent{}).Error()
	if err != "request blocked by WAF" {
		t.Fatalf("unexpected error message: %q", err)
	}
}

func TestIsSecurityError(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if IsSecurityError(nil) {
			t.Fatal("expected nil error to not be a security error")
		}
	})

	t.Run("security error", func(t *testing.T) {
		err := &BlockingSecurityEvent{}
		if !IsSecurityError(err) {
			t.Fatal("expected BlockingSecurityEvent to be detected as security error")
		}
	})

	t.Run("wrapped security error", func(t *testing.T) {
		err := errors.New("outer")
		wrapped := errors.Join(err, &BlockingSecurityEvent{})
		if !IsSecurityError(wrapped) {
			t.Fatal("expected wrapped BlockingSecurityEvent to be detected as security error")
		}
	})

	t.Run("non security error", func(t *testing.T) {
		err := errors.New("not security")
		if IsSecurityError(err) {
			t.Fatal("expected non-security error to not be detected as security error")
		}
	})
}
