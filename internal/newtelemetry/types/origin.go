// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package types

import (
	"bytes"
	"fmt"
)

type Origin int

const (
	OriginDefault Origin = iota
	OriginCode
	OriginDDConfig
	OriginEnvVar
	OriginRemoteConfig
)

func (o Origin) String() string {
	switch o {
	case OriginDefault:
		return "default"
	case OriginCode:
		return "code"
	case OriginDDConfig:
		return "dd_config"
	case OriginEnvVar:
		return "env_var"
	case OriginRemoteConfig:
		return "remote_config"
	default:
		return fmt.Sprintf("unknown origin %d", o)
	}
}

func (o Origin) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`"`)
	b.WriteString(o.String())
	b.WriteString(`"`)
	return b.Bytes(), nil
}
