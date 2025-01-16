// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package newtelemetry

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

type products struct {
	mu       sync.Mutex
	products map[types.Namespace]transport.Product
	size     int
}

func (p *products) Add(namespace types.Namespace, enabled bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.products == nil {
		p.products = make(map[types.Namespace]transport.Product)
	}

	product := transport.Product{
		Enabled: enabled,
	}

	if err != nil {
		product.Error = transport.Error{
			Message: err.Error(),
		}
	}

	if product, ok := p.products[namespace]; ok {
		p.size -= len(namespace) + len(product.Error.Message)
	}

	p.products[namespace] = product
	p.size += len(namespace) + len(product.Error.Message)
}

func (p *products) Payload() transport.Payload {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.products) == 0 {
		return nil
	}

	res := transport.AppProductChange{
		Products: p.products,
	}
	p.products = nil
	p.size = 0
	return res
}

func (p *products) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.size
}
