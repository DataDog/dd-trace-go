// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sharedsec

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewBlockRequestAction(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/json", NewBlockRequestAction(403, 10, "json").HTTP().ServeHTTP)
	mux.HandleFunc("/html", NewBlockRequestAction(403, 10, "html").HTTP().ServeHTTP)
	mux.HandleFunc("/auto", NewBlockRequestAction(403, 10, "auto").HTTP().ServeHTTP)
	defer srv.Close()

	t.Run("json", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			accept string
		}{
			{
				name: "no-accept",
			},
			{
				name:   "irrelevant-accept",
				accept: "text/html",
			},
			{
				name:   "accept",
				accept: "application/json",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req, err := http.NewRequest("POST", srv.URL+"/json", nil)
				req.Header.Set("Accept", tc.accept)
				require.NoError(t, err)
				res, err := srv.Client().Do(req)
				require.NoError(t, err)
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				require.Equal(t, 403, res.StatusCode)
				require.Equal(t, blockedTemplateJSON, body)
			})
		}
	})

	t.Run("html", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			accept string
		}{
			{
				name: "no-accept",
			},
			{
				name:   "irrelevant-accept",
				accept: "application/json",
			},
			{
				name:   "accept",
				accept: "text/html",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req, err := http.NewRequest("POST", srv.URL+"/html", nil)
				require.NoError(t, err)
				res, err := srv.Client().Do(req)
				require.NoError(t, err)
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				require.Equal(t, 403, res.StatusCode)
				require.Equal(t, blockedTemplateHTML, body)
			})
		}
	})

	t.Run("auto", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			accept   string
			expected []byte
		}{
			{
				name:     "no-accept",
				expected: blockedTemplateJSON,
			},
			{
				name:     "json-accept-1",
				accept:   "application/json",
				expected: blockedTemplateJSON,
			},
			{
				name:     "json-accept-2",
				accept:   "application/json,text/html",
				expected: blockedTemplateJSON,
			},
			{
				name:     "json-accept-3",
				accept:   "irrelevant/content,application/json,text/html",
				expected: blockedTemplateJSON,
			},
			{
				name:     "json-accept-4",
				accept:   "irrelevant/content,application/json,text/html,application/json",
				expected: blockedTemplateJSON,
			},
			{
				name:     "html-accept-1",
				accept:   "text/html",
				expected: blockedTemplateHTML,
			},
			{
				name:     "html-accept-2",
				accept:   "text/html,application/json",
				expected: blockedTemplateHTML,
			},
			{
				name:     "html-accept-3",
				accept:   "irrelevant/content,text/html,application/json",
				expected: blockedTemplateHTML,
			},
			{
				name:     "html-accept-4",
				accept:   "irrelevant/content,text/html,application/json,text/html",
				expected: blockedTemplateHTML,
			},
			{
				name:     "irrelevant-accept",
				accept:   "irrelevant/irrelevant,application/html",
				expected: blockedTemplateJSON,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req, err := http.NewRequest("POST", srv.URL+"/auto", nil)
				req.Header.Set("Accept", tc.accept)
				require.NoError(t, err)
				res, err := srv.Client().Do(req)
				require.NoError(t, err)
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				require.Equal(t, 403, res.StatusCode)
				require.Equal(t, tc.expected, body)
			})
		}
	})
}

func TestNewRedirectRequestAction(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/redirect1", NewRedirectRequestAction(http.StatusFound, "/redirect2").HTTP().ServeHTTP)
	mux.HandleFunc("/redirect2", NewRedirectRequestAction(http.StatusFound, "/redirected").HTTP().ServeHTTP)
	mux.HandleFunc("/redirected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // Shouldn't matter since we write 302 before arriving here
		w.Write([]byte("Redirected"))
	})
	srv.Client().CheckRedirect = func(req *http.Request, via []*http.Request) error {
		require.GreaterOrEqual(t, len(via), 1)
		require.Equal(t, "/redirect1", via[0].URL.Path)
		if len(via) == 2 {
			require.Equal(t, "/redirect2", via[1].URL.Path)
			require.NotNil(t, via[1].Response)
			require.Equal(t, http.StatusFound, via[1].Response.StatusCode)
		}
		return nil
	}
	defer srv.Close()

	for _, tc := range []struct {
		name string
		url  string
	}{
		{
			name: "no-redirect",
			url:  "/redirected",
		},
		{
			name: "redirect",
			url:  "/redirect1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("POST", srv.URL+tc.url, nil)
			require.NoError(t, err)
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "Redirected", string(body))
		})
	}

}
