// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !windows
// +build !windows

package ddtrace

import "time"

// TODO(kjn v2): Get rid of exported NowTime()
// NowTime returns the current time, as computed by Time.Now().
var NowTime = func() time.Time { return time.Now() }

// TODO(kjn v2): Get rid of exported Now()
// Now returns the current UNIX time in nanoseconds, as computed by Time.UnixNano().
var Now = func() int64 { return time.Now().UnixNano() }
