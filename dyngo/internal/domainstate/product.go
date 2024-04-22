// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package domainstate

import "gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"

// Product is a component that is interested in listening to one or more
// domain's operation & data events.
type Product interface {
	// Name is the name of the product. Product names should strive to be unique,
	// and should remain human-readable as they may be displayed in log entries.
	Name() string

	// Start is called when a new root operation is created, and provides this
	// Product a chance to install its listeners into that new root operation.
	Start(root operation.Operation)
}
