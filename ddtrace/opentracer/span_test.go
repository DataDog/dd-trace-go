// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpanBaggage(t *testing.T) {
	assert := assert.New(t)
	ot := New()
	ott, ok := ot.(*opentracer)
	assert.True(ok)
	s := ott.StartSpan("test.operation")
	ss, ok := s.(*span)
	assert.True(ok)
	ss.SetBaggageItem("foo", "bar")
	assert.Equal(t, "bar", ss.BaggageItem("foo"))
}
