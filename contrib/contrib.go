// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// The purpose of the packages held in contrib is to provide tracing on top of commonly used
// packages from the standard library as well as the community in a "plug-and-play" manner.
// This means that by simply importing the appropriate path, functions are exposed having
// the same signature as the original package. These functions return structures that embed
// the original return value, allowing  them to be used as they normally would with tracing
// activated out of the box.

// All of these libraries are supported by our https://www.datadoghq.com/apm/.
package contrib
