// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

// Backlog represents the size of a queue that hasn't been yet read by the consumer.
type Backlog struct {
	// Tags that identify the backlog
	Tags []string
	// Value of the backlog
	Value int64
}
