// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

// startForeignThreadSignals is a no-op on Windows. WithForeignThreadSignals
// documents a POSIX pthread/signal model (a thread created entirely by
// native code, with no saved handler and no signal.Notify, terminates the
// process silently) that does not apply on Windows: a thread created by
// native code there still faults through the same structured-exception
// mechanism errorType's WindowsException classification already covers, not
// a separate, invisible path SetCrashOutput cannot see.
func startForeignThreadSignals(_ *config) {}
