// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package os

import (
	"os"
	"os/exec"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
)

// InjectSessionEnv injects stable session identifier environment variables into
// the given [exec.Cmd] so that child processes inherit the root session ID.
// If cmd.Env is nil, it copies [os.Environ] first. This should be called after
// configuring the command but before calling cmd.Start() or cmd.Run().
func InjectSessionEnv(cmd *exec.Cmd) {
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env,
		"DD_ROOT_GO_SESSION_ID="+globalconfig.RootSessionID(),
	)
}
