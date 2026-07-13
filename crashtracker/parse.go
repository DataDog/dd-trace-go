// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

// parseCrashDump parses a raw Go crash dump into a Report.
// The input is the full text written by the Go runtime to the crash output fd.
func parseCrashDump(dump []byte) *Report {
	panic("not implemented") // WS-B implements this
}
