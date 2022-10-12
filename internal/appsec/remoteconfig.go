// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"encoding/json"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

func genApplyStatus(ack bool, err error) rc.ApplyStatus {
	status := rc.ApplyStatus{
		State: rc.ApplyStateUnacknowledged,
	}
	if err != nil {
		status.State = rc.ApplyStateError
		status.Error = err.Error()
	} else if ack {
		status.State = rc.ApplyStateAcknowledged
	}

	return status
}

func defaultStatusesFromUpdate(u remoteconfig.ProductUpdate, ack bool) map[string]rc.ApplyStatus {
	statuses := make(map[string]rc.ApplyStatus, len(u))
	for path := range u {
		statuses[path] = genApplyStatus(ack, nil)
	}
	return statuses
}

// asmFeaturesCallback deserializes an ASM_FEATURES configuration received through remote config
// and starts/stops appsec accordingly. Used as a callback for the ASM_FEATURES remote config product.
func (a *appsec) asmFeaturesCallback(u remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
	statuses := defaultStatusesFromUpdate(u, false)
	if l := len(u); l > 1 {
		log.Error("appsec: Remote config: %d configs received for ASM_FEATURES. Expected one at most, returning early", l)
		return statuses
	}
	for path, raw := range u {
		var data rc.ASMFeaturesData
		status := rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
		var err error
		log.Debug("appsec: Remote config: processing %s", path)

		// A nil config means ASM was disabled, and we stopped receiving the config file
		// Don't ack the config in this case and return early
		if raw == nil {
			log.Debug("appsec: Remote config: Stopping AppSec")
			a.stop()
			return statuses
		}
		if err = json.Unmarshal(raw, &data); err != nil {
			log.Error("appsec: Remote config: error while unmarshalling %s: %v. Configuration won't be applied.", path, err)
		} else if data.ASM.Enabled && !a.started {
			log.Debug("appsec: Remote config: Starting AppSec")
			if err = a.start(); err != nil {
				log.Error("appsec: Remote config: error while processing %s. Configuration won't be applied: %v", path, err)
			}
		} else if !data.ASM.Enabled && a.started {
			log.Debug("appsec: Remote config: Stopping AppSec")
			a.stop()
		}
		if err != nil {
			status.State = rc.ApplyStateError
			status.Error = err.Error()
		}
		statuses[path] = status
	}

	return statuses
}

func asmDataCallback(u remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
	// Following the RFC, merging should only happen when two rules data with the same ID and same Type are received
	// rulesData[ID][Type] will return the rules data of said id and type, if it exists
	allRulesData := make(map[string]map[string]rc.ASMDataRuleData)

	for path, raw := range u {
		log.Debug("appsec: remoteconfig: processing %s", path)
		var rulesData rc.ASMDataRulesData
		if err := json.Unmarshal(raw, &rulesData); err != nil {
			log.Debug("appsec: remoteconfig: error while unmarshalling payload for %s", path)
			continue
		}

		for _, ruleData := range rulesData.RulesData {
			if allRulesData[ruleData.ID] == nil {
				allRulesData[ruleData.ID] = make(map[string]rc.ASMDataRuleData)
			}
			if data, ok := allRulesData[ruleData.ID][ruleData.Type]; ok {
				allRulesData[ruleData.ID][ruleData.Type] = mergeRulesData(ruleData, data)
			} else {
				allRulesData[ruleData.ID][ruleData.Type] = ruleData
			}
		}
	}

	var rulesData []rc.ASMDataRuleData
	for _, m := range allRulesData {
		for _, data := range m {
			rulesData = append(rulesData, data)
		}
	}

	if _, err := json.Marshal(rc.ASMDataRulesData{RulesData: rulesData}); err != nil {
		log.Debug("appsec: remoteconfig: could not marshal the merged rules data")
	}

	// TODO: pass payload to WAF using waf.Handle.UpdateRulesData()
	return nil
}

// mergeRulesData merges two rules data files together, removing duplicates and
// only keeping the most up-to-date values
// It currently bypasses the second argument and returns the 1st as is
func mergeRulesData(data1, _ rc.ASMDataRuleData) rc.ASMDataRuleData {
	return data1
}

func (a *appsec) startRC() {
	if a.rc != nil {
		a.rc.Start()
	}
}

func (a *appsec) stopRC() {
	if a.rc != nil {
		a.rc.Stop()
	}
}

func (a *appsec) registerRCProduct(product string) error {
	if a.rc == nil {
		return fmt.Errorf("no valid remote configuration client")
	}
	// Don't do anything if the product is already registered
	for _, p := range a.rc.Products {
		if p == product {
			return nil
		}
	}
	a.rc.Products = append(a.rc.Products, product)
	return nil
}
func (a *appsec) registerRCCapability(c remoteconfig.Capability) error {
	if a.rc == nil {
		return fmt.Errorf("no valid remote configuration client")
	}
	// Don't do anything if the capability is already registered
	for _, cap := range a.rc.Capabilities {
		if cap == c {
			return nil
		}
	}
	a.rc.Capabilities = append(a.rc.Capabilities, c)
	return nil
}

func (a *appsec) registerRCCallback(c remoteconfig.Callback, product string) error {
	if a.rc != nil {
		return fmt.Errorf("no valid remote configuration client")
	}
	a.rc.RegisterCallback(c, product)
	return nil

}

func (a *appsec) enableRemoteActivation() error {
	if a.rc == nil {
		return fmt.Errorf("no valid remote configuration client")
	}
	// First verify that the WAF is in good health. We perform this check in order not to falsely "allow" users to
	// activate ASM through remote config if activation would fail when trying to register a WAF handle
	// (ex: if the service runs on an unsupported platform).
	if err := waf.Health(); err != nil {
		log.Debug("appsec: WAF health check failed, remote activation will be disabled: %v", err)
		return err
	}
	a.registerRCProduct(rc.ProductASMFeatures)
	a.registerRCCapability(remoteconfig.ASMActivation)
	a.registerRCCallback(a.asmFeaturesCallback, rc.ProductASMFeatures)
	return nil
}
