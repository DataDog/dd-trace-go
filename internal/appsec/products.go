// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package appsec

import (
	"errors"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type NewProduct func(*config.Config, dyngo.Operation) (Product, error)

type Product interface {
	Stop()
}

var products = map[string]NewProduct{
	"WAF": NewWAF,
}

func (a *appsec) SwapRootOperation() error {
	newRoot := dyngo.NewRootOperation()
	newProducts := make([]Product, 0, len(products))
	var productErrors []error
	for name, newProduct := range products {
		product, err := newProduct(a.cfg, newRoot)
		if err != nil {
			productErrors = append(productErrors, fmt.Errorf("error creating %s listeners: %w", name, err))
			continue
		}

		newProducts = append(newProducts, product)
	}

	a.productsMu.Lock()
	defer a.productsMu.Unlock()

	oldProducts := a.products
	a.products = newProducts

	dyngo.SwapRootOperation(newRoot)

	for _, product := range oldProducts {
		product.Stop()
	}

	return errors.Join(productErrors...)
}
