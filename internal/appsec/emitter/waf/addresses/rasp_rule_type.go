// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package addresses

import (
	waf "github.com/DataDog/go-libddwaf/v3"
)

type RASPRuleType string

const (
	RASPRuleTypeLFI  RASPRuleType = "lfi"
	RASPRuleTypeSSRF RASPRuleType = "ssrf"
	RASPRuleTypeSQLI RASPRuleType = "sql_injection"
	RASPRuleTypeCMDI RASPRuleType = "command_injection"
)

func RASPRuleTypes() []RASPRuleType {
	return []RASPRuleType{
		RASPRuleTypeLFI,
		RASPRuleTypeSSRF,
		RASPRuleTypeSQLI,
		RASPRuleTypeCMDI,
	}
}

// RASPRuleTypeFromAddressSet returns the RASPRuleType for the given address set if it has a RASP address.
func RASPRuleTypeFromAddressSet(addressSet waf.RunAddressData) (RASPRuleType, bool) {
	if addressSet.Scope != waf.RASPScope {
		return "", false
	}

	for address := range addressSet.Ephemeral {
		switch address {
		case ServerIOFSFileAddr:
			return RASPRuleTypeLFI, true
		case ServerIoNetURLAddr:
			return RASPRuleTypeSSRF, true
		case ServerDBStatementAddr, ServerDBTypeAddr:
			return RASPRuleTypeSQLI, true
		case ServerSysExecCmd:
			return RASPRuleTypeCMDI, true
		}
	}

	return "", false
}
