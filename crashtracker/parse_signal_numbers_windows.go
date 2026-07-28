// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

// signalNumbers is empty on Windows: the Go runtime's crash dump text never
// contains a POSIX "SIG*" line on this platform (Windows crashes are reported
// as structured exceptions, not Unix signals), so there is nothing to map.
// Any lookup falls back to the zero value.
var signalNumbers = map[string]int{}
