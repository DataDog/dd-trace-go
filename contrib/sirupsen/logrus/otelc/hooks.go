// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for github.com/sirupsen/logrus.
// See otelc.yaml. It is a separate module so otelc never reaches contrib users
// who do not use it.
package otelc

import (
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otelc/pkg/hook"

	logrustrace "github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2"
)

// AfterNew adds the contrib's hook to the logger logrus.New returns.
//
// logrus runs New for its standard logger before this package is initialized,
// so this must not read package state.
func AfterNew(_ hook.HookContext, logger *logrus.Logger) {
	if logger == nil {
		return
	}
	logger.AddHook(&logrustrace.DDContextLogHook{})
}

// BeforeLevelHooksAdd skips the call when it would add a second
// DDContextLogHook to hooks.
func BeforeLevelHooksAdd(ictx hook.HookContext, hooks logrus.LevelHooks, h logrus.Hook) {
	if _, ok := h.(*logrustrace.DDContextLogHook); !ok {
		return
	}
	if hasContextLogHook(hooks) {
		ictx.SetSkipCall(true)
	}
}

func hasContextLogHook(hooks logrus.LevelHooks) bool {
	for _, levelHooks := range hooks {
		for _, h := range levelHooks {
			if _, ok := h.(*logrustrace.DDContextLogHook); ok {
				return true
			}
		}
	}
	return false
}
