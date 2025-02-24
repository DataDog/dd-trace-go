// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package orchestrion

import (
	"fmt"
	"net/http"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"
)

func WrapHandler(handler http.Handler) http.Handler {
	switch handler := handler.(type) {
	case *wrap.ServeMux, wrap.WrappedHandler:
		return handler
	case *http.ServeMux:
		tracedMux := wrap.NewServeMux()
		tracedMux.ServeMux = handler
		return tracedMux
	default:
		return wrap.Handler(handler, "", "", config.WithResourceNamer(resourceNamer))
	}
}

func resourceNamer(r *http.Request) string {
	return fmt.Sprintf("%s %s", r.Method, r.URL.Path)
}
