// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	logrustrace "github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2"
)

// add calls hooks.Add the way the trampoline does with BeforeLevelHooksAdd.
func add(hooks logrus.LevelHooks, h logrus.Hook) {
	ictx := hooktest.NewMockHookContext(hooks, h)
	BeforeLevelHooksAdd(ictx, hooks, h)
	if !ictx.IsSkipCall() {
		hooks.Add(h)
	}
}

type otherHook struct{}

func (otherHook) Levels() []logrus.Level   { return logrus.AllLevels }
func (otherHook) Fire(*logrus.Entry) error { return nil }

func TestBeforeLevelHooksAdd(t *testing.T) {
	hooks := make(logrus.LevelHooks)
	add(hooks, otherHook{})
	add(hooks, &logrustrace.DDContextLogHook{})
	add(hooks, &logrustrace.DDContextLogHook{})
	add(hooks, otherHook{})

	for _, level := range logrus.AllLevels {
		assert.Len(t, hooks[level], 3, "level %s", level)
		assert.IsType(t, otherHook{}, hooks[level][0])
		assert.IsType(t, &logrustrace.DDContextLogHook{}, hooks[level][1])
		assert.IsType(t, otherHook{}, hooks[level][2])
	}
}

func TestAfterNew(t *testing.T) {
	logger := &logrus.Logger{Hooks: make(logrus.LevelHooks)}
	AfterNew(hooktest.NewMockHookContext(), logger)
	for _, level := range logrus.AllLevels {
		assert.Len(t, logger.Hooks[level], 1, "level %s", level)
		assert.IsType(t, &logrustrace.DDContextLogHook{}, logger.Hooks[level][0])
	}

	AfterNew(hooktest.NewMockHookContext(), nil)
}
