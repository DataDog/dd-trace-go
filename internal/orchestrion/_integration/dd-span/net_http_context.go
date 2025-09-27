// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package ddspan

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseNetHTTPContext struct {
	srv  *http.Server
	addr string
	wg   sync.WaitGroup
}

func (tc *TestCaseNetHTTPContext) Setup(_ context.Context, t *testing.T) {
	tc.addr = fmt.Sprintf("127.0.0.1:%d", net.FreePort(t))

	tc.srv = &http.Server{
		Addr:         tc.addr,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	//dd:span span.name:net/http.rootHandler resource.name:rootHandler
	rootHandler := func(w http.ResponseWriter, req *http.Request) {
		tc.wg.Add(2)

		go func() {
			defer tc.wg.Done()
			time.Sleep(2 * time.Second)
			backgroundFunc(req.Context())
		}()
		go func() {
			defer tc.wg.Done()
			time.Sleep(1 * time.Second)
			backgroundFunc(req.Context())
		}()

		fmt.Fprint(w, "Hello World")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", rootHandler)

	tc.srv.Handler = mux

	go func() { assert.ErrorIs(t, tc.srv.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.srv.Shutdown(ctx))
	})
}

func (tc *TestCaseNetHTTPContext) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.addr + "/")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	tc.wg.Wait()
}

func (tc *TestCaseNetHTTPContext) ExpectedTraces() trace.Traces {
	wantTraces := `
[http.request | GET / | net/http | client]
	[http.request | GET / | net/http | server]
		[net/http.rootHandler | rootHandler]
			[backgroundFunc | backgroundFunc]
			[backgroundFunc | backgroundFunc]
`

	return trace.FromSimplified(wantTraces)
}

//dd:span
func backgroundFunc(ctx context.Context) {}
