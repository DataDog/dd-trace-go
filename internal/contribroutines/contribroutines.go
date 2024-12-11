// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package contribroutines

import "sync"

var (
	stop chan struct{} = make(chan struct{})
	once sync.Once
)

func Stop() {
	once.Do(func() {
		close(stop)
	})
}

func GetStopChan() chan struct{} {
	return stop
}
