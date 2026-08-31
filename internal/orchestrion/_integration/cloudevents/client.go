// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cloudevents

import (
	"context"

	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/client"
)

type sender struct{}

func (sender) Send(context.Context, binding.Message, ...binding.Transformer) error {
	return nil
}

// newClient deliberately imports only the client subpackage. This verifies the
// Orchestrion guard does not suppress instrumentation in user code with this
// import shape.
func newClient() (client.Client, error) {
	return client.New(sender{})
}
