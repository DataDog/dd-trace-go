// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package telemetry

import (
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

var (
	appEndpointsMessageLimit     = 300
	appEndpointsMessageLimitOnce sync.Once
)

// SetAppEndpointsMessageLimit supplies the singleton-backed process value
// without introducing an import cycle.
func SetAppEndpointsMessageLimit(limit int) {
	appEndpointsMessageLimitOnce.Do(func() {
		appEndpointsMessageLimit = limit
	})
}

type appEndpointKey struct {
	OperationName string
	ResourceName  string
}

type appEndpoints struct {
	mu    sync.Mutex
	store []transport.AppEndpoint
	// isFirst determines what the first message for a deployment is, allowing
	// multiple messages to be accumulated backend-side into a single API
	// definition; while allowing new deployments to drop removed APIs.
	isFirst bool
}

func (a *appEndpoints) Add(opName string, resName string, attrs AppEndpointAttributes) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.store = append(a.store, transport.AppEndpoint{
		OperationName:    opName,
		ResourceName:     resName,
		Kind:             attrs.Kind,
		Method:           attrs.Method,
		Path:             attrs.Path,
		RequestBodyType:  attrs.RequestBodyType,
		ResponseBodyType: attrs.ResponseBodyType,
		ResponseCode:     attrs.ResponseCode,
		Authentication:   attrs.Authentication,
		Metadata:         attrs.Metadata,
	})
}

func (a *appEndpoints) Payload() transport.Payload {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.store) == 0 {
		return nil
	}

	count := min(len(a.store), appEndpointsMessageLimit)
	payload := &transport.AppEndpoints{
		IsFirst:   a.isFirst,
		Endpoints: a.store[:count],
	}

	a.isFirst = false
	if count < len(a.store) {
		a.store = a.store[count:]
	} else {
		a.store = nil
	}

	return payload
}
