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

func TestNewHTTPBlockRequestAction(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/json", newHTTPBlockRequestAction(403, "json").ServeHTTP)
	mux.HandleFunc("/html", newHTTPBlockRequestAction(403, "html").ServeHTTP)
	mux.HandleFunc("/auto", newHTTPBlockRequestAction(403, "auto").ServeHTTP)
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
	mux.HandleFunc("/redirect-default-status", newRedirectRequestAction(100, "/redirected").ServeHTTP)
	mux.HandleFunc("/redirect-no-location", newRedirectRequestAction(303, "").ServeHTTP)
	mux.HandleFunc("/redirect1", newRedirectRequestAction(http.StatusFound, "/redirect2").ServeHTTP)
	mux.HandleFunc("/redirect2", newRedirectRequestAction(http.StatusFound, "/redirected").ServeHTTP)
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

	// These tests check that redirect actions can handle bad parameter values
	// - empty location: revert to default blocking action instead
	// - status code outside of [300, 399]: default to 303
	t.Run("no-location", func(t *testing.T) {
		srv.Client().CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return nil
		}
		req, err := http.NewRequest("POST", srv.URL+"/redirect-no-location", nil)
		require.NoError(t, err)
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, blockedTemplateJSON, body)
	})

	t.Run("bad-status-code", func(t *testing.T) {
		srv.Client().CheckRedirect = func(req *http.Request, via []*http.Request) error {
			require.Equal(t, len(via), 1)
			require.Equal(t, "/redirect-default-status", via[0].URL.Path)
			require.Equal(t, 303, req.Response.StatusCode)
			return nil
		}
		req, err := http.NewRequest("POST", srv.URL+"/redirect-default-status", nil)
		require.NoError(t, err)
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		require.Equal(t, "Redirected", string(body))
	})
}

func TestNewBlockParams(t *testing.T) {
	for name, tc := range map[string]struct {
		params   map[string]any
		expected blockActionParams
	}{
		"block-1": {
			params: map[string]any{
				"status_code": "403",
				"type":        "auto",
			},
			expected: blockActionParams{
				Type:       "auto",
				StatusCode: 403,
			},
		},
		"block-2": {
			params: map[string]any{
				"status_code": "405",
				"type":        "html",
			},
			expected: blockActionParams{
				Type:       "html",
				StatusCode: 405,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			actionParams, err := blockParamsFromMap(tc.params)
			require.NoError(t, err)
			require.Equal(t, tc.expected.Type, actionParams.Type)
			require.Equal(t, tc.expected.StatusCode, actionParams.StatusCode)
		})
	}
}

func TestNewRedirectParams(t *testing.T) {
	for name, tc := range map[string]struct {
		params   map[string]any
		expected redirectActionParams
	}{
		"redirect-1": {
			params: map[string]any{
				"status_code": "308",
				"location":    "/redirected",
			},
			expected: redirectActionParams{
				Location:   "/redirected",
				StatusCode: 308,
			},
		},
		"redirect-2": {
			params: map[string]any{
				"status_code": "303",
				"location":    "/tmp",
			},
			expected: redirectActionParams{
				Location:   "/tmp",
				StatusCode: 303,
			},
		},
		"no-location": {
			params: map[string]any{
				"status_code": "303",
			},
			expected: redirectActionParams{
				Location:   "",
				StatusCode: 303,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			actionParams, err := redirectParamsFromMap(tc.params)
			require.NoError(t, err)
			require.Equal(t, tc.expected, actionParams)
		})
	}
}
