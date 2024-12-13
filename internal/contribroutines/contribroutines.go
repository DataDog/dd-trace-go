// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package contribroutines

import "sync"

var (
	stop chan struct{} = make(chan struct{})
	once sync.Once
	mu   sync.Mutex
)

func Stop() {
	once.Do(func() {
		mu.Lock()
		defer mu.Unlock()
		close(stop)
	})
}

func GetStopChan() chan struct{} {
	mu.Lock()
	defer mu.Unlock()
	return stop
}
