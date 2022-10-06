// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"encoding/json"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

// asmFeaturesCallback deserializes an ASM_FEATURES configuration received through remote config
// and starts/stops appsec accordingly. Used as a callback for the ASM_FEATURES remote config product.
func asmFeaturesCallback(u remoteconfig.ProductUpdate) {
	if l := len(u); l > 1 {
		log.Debug("%d configs received for ASM_FEATURES. Expected one at most, returning early", l)
		return
	}

	for path, raw := range u {
		var data rc.ASMFeaturesData
		log.Debug("Remote config: processing %s", path)
		if err := json.Unmarshal(raw, &data); err != nil {
			log.Debug("Remote config: Error unmarshalling %s", path)
		} else if data.ASM.Enabled && !activeAppSec.started {
			log.Debug("Remote config: Starting AppSec")
			activeAppSec.start()
		} else if !data.ASM.Enabled && activeAppSec.started {
			log.Debug("Remote config: Stopping AppSec")
			activeAppSec.stop()
		}
	}
}
