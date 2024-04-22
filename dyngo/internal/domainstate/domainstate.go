// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package domainstate

import (
	"sort"
	"sync"

	"github.com/gofiber/fiber/v2/log"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"
)

//go:generate go run golang.org/x/tools/cmd/stringer@latest --type=Domain

// Domain represents an operations domain (e.g, HTTP, gRPC, GraphQL, etc...).
type Domain uint8

const (
	Performance Domain = iota // Arbitrary performance data (e.g, custom spans)
	HTTP                      // The HTTP domain
	GRPC                      // The gRPC domain
	GraphQL                   // The GraphQL domain

	// The amount of domains available
	count int = iota
)

type domainState struct {
	products          map[int][]Product
	productPriorities []int
	active            bool
	mu                sync.RWMutex
}

var (
	domains [count]Domain // Filled by `init()`
	state   [count]domainState
)

// AllDomains returns a slice containing all available domains, regardless of
// their activation status.
func AllDomains() []Domain {
	// We return by copy to avoid mutations causing issues...
	result := make([]Domain, count)
	copy(result, domains[:])
	return result
}

// RegisterProduct adds the provided product to the receiving Domain with the
// specified priority. If the domain is already active, the product is started
// immediately. Otherwise, products are started in ascending priority order
// (two products with identical priority are started in some arbitrary order)
// when the domain is activated.
func (d Domain) RegisterProduct(prod Product, prio int) {
	d.registerProduct(prod, prio)
	if d.isActive() {
		if root := operation.CurrentRoot(); root != nil {
			log.Tracef("dyngo.%s: domain is active, immediately starting product %q\n", d, prod.Name())
			prod.Start(root)
		}
	}
}

// StartProducts starts all products registered in the provided domain with the
// specified root operaton, if the domain is currently active.
func StartProducts(dom Domain, root operation.Operation) {
	state[dom].mu.RLock()
	defer state[dom].mu.RUnlock()

	if !state[dom].active || len(state[dom].products) == 0 {
		return
	}

	for _, prio := range state[dom].productPriorities {
		for _, product := range state[dom].products[prio] {
			log.Tracef("dyngo.%s: starting product at priority %d: %s\n", dom, prio, product.Name())
			product.Start(root)
		}
	}
}

// Activate marks the receiving Domain as active, allowing interested products
// to register themselves into the root operation.Operation.
func (d Domain) Activate() {
	state[d].mu.Lock()
	defer state[d].mu.Unlock()

	if !state[d].active {
		log.Tracef("Activating domain dyngo.%s...\n", d)
		state[d].active = true
	}
}

// isActive determines whether this Domain is currently marked as active.
func (d Domain) isActive() bool {
	state[d].mu.RLock()
	defer state[d].mu.RUnlock()

	return state[d].active
}

// registerProduct registers a product in the state of the receiving Domain, but
// does not start it, even if the domain is already activated.
func (d Domain) registerProduct(prod Product, prio int) {
	state[d].mu.Lock()
	defer state[d].mu.Unlock()

	if state[d].products == nil {
		state[d].products = map[int][]Product{prio: {prod}}
		state[d].productPriorities = []int{prio}
	} else {
		existing := state[d].products[prio]
		state[d].products[prio] = append(existing, prod)
		if len(existing) == 0 {
			// We have registered under some new priority, register it in the list...
			state[d].productPriorities = append(state[d].productPriorities, prio)
			// Keep our priorities in order...
			sort.Ints(state[d].productPriorities)
		}
	}
}

func init() {
	for dom := 0; dom < count; dom++ {
		domains[dom] = Domain(dom)
	}
}
