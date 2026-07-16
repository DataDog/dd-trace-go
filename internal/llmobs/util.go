// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

// agentNameWireSafe reports whether name can be safely written as an
// x-datadog-tags tag value. The rules match the shared cross-language contract:
//
//   - reject if the name exceeds 256 bytes
//   - reject any byte outside the printable ASCII range [0x20, 0x7E]
//   - reject a comma (the tagset delimiter)
//
// An equals sign is legal in tagset values (only illegal in keys) and is NOT
// rejected. This gate applies only to the wire tag; meta.agent_attribution
// always uses the real name.
func agentNameWireSafe(name string) bool {
	if len(name) > 256 {
		return false
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b < 0x20 || b > 0x7E {
			return false
		}
		if b == ',' {
			return false
		}
	}
	return true
}
