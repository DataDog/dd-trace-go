// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !linux

package crashtracker

import "os/exec"

func retainSupervisorThread(_ *exec.Cmd) func() {
	return func() {}
}

func supervisorNotifyRelay(_ *exec.Cmd) (start func(appPID int), stop func(), err error) {
	return func(int) {}, func() {}, nil
}

func supervisorReapOrphans(_ int) func() {
	return func() {}
}
