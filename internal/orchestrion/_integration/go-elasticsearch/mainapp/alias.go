// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under other names than elasticsearch, which the rules must handle
// the way orchestrion does.
import (
	es6 "github.com/elastic/go-elasticsearch/v6"
	es7 "github.com/elastic/go-elasticsearch/v7"
	es8 "github.com/elastic/go-elasticsearch/v8"
)

func performAliased(addrs []string) {
	perform(es6.NewClient(es6.Config{Addresses: addrs}))("main-alias-v6-new-client")
	perform(es7.NewClient(es7.Config{Addresses: addrs}))("main-alias-v7-new-client")
	perform(es8.NewClient(es8.Config{Addresses: addrs}))("main-alias-v8-new-client")
}
