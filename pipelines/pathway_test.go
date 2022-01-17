// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package pipelines

import (
	"testing"
	"time"
)

func TestPathway(t *testing.T) {
	start := time.Now()
	p := newPathway(start)
	end := start.Add(time.Hour)
	p.setCheckpoint("edge-1", end)
}