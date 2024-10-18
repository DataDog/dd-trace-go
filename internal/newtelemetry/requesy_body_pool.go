// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	tracerversion "gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

type RequestBodyPool struct {
	pool  sync.Pool
	seqID atomic.Int64
}

func NewRequestPool(service, env, version string) *RequestBodyPool {
	return &RequestBodyPool{
		pool: sync.Pool{
			New: func() any {
				return &Body{
					APIVersion: "v2",
					RuntimeID:  globalconfig.RuntimeID(),
					Application: Application{
						ServiceName:     service,
						Env:             env,
						ServiceVersion:  version,
						TracerVersion:   tracerversion.Tag,
						LanguageName:    "go",
						LanguageVersion: runtime.Version(),
					},
					Host: Host{
						Hostname:      hostname.Get(),
						OS:            osinfo.OSName(),
						OSVersion:     osinfo.OSVersion(),
						Architecture:  osinfo.Architecture(),
						KernelName:    osinfo.KernelName(),
						KernelRelease: osinfo.KernelRelease(),
						KernelVersion: osinfo.KernelVersion(),
					},
				}
			},
		},
	}
}

// Get returns a new Body from the pool, ready to be turned into JSON and sent to the API.
func (p *RequestBodyPool) Get(payload Payload) *Body {
	body := p.pool.Get().(*Body)
	body.SeqID = p.seqID.Add(1)
	body.TracerTime = time.Now().Unix()
	body.Payload = payload
	body.RequestType = payload.RequestType()
	return body
}

// Put returns a Body to the pool.
func (p *RequestBodyPool) Put(body *Body) {
	body.Payload = nil
	p.pool.Put(body)
}
