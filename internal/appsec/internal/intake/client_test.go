// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package intake

import (
	"context"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClient(t *testing.T) {
	t.Run("NewClient", func(t *testing.T) {
		t.Run("default http client", func(t *testing.T) {
			client, err := NewClient(nil, "target")
			require.NoError(t, err)
			require.NotNil(t, client)
		})

		t.Run("specific http client", func(t *testing.T) {
			httpClient := &http.Client{
				Timeout: 10 * time.Second,
			}
			client, err := NewClient(httpClient, "target")
			require.NoError(t, err)
			require.NotNil(t, client)
		})
	})

	t.Run("newRequest", func(t *testing.T) {
		baseURL := "http://target/"
		c, err := NewClient(nil, baseURL)
		require.NoError(t, err)

		for _, tc := range []struct {
			name         string
			endpoint     string
			method       string
			body         interface{}
			wantError    bool
			expectedBody string
		}{
			{
				name:      "get /endpoint without body",
				endpoint:  "endpoint",
				method:    http.MethodGet,
				body:      nil,
				wantError: false,
			},
			{
				name:      "post /endpoint without body",
				endpoint:  "endpoint",
				method:    http.MethodPost,
				body:      nil,
				wantError: false,
			},
			{
				name:      "bad method",
				endpoint:  "endpoint",
				method:    ";",
				body:      nil,
				wantError: true,
			},
			{
				name:      "bad endpoint",
				endpoint:  ":endpoint",
				method:    "GET",
				body:      nil,
				wantError: true,
			},
			{
				name:      "bad endpoint",
				endpoint:  ":endpoint",
				method:    "GET",
				body:      nil,
				wantError: true,
			},
			{
				name:         "post /version/endpoint with body",
				endpoint:     "version/endpoint",
				method:       http.MethodPost,
				body:         []string{"a", "b", "c"},
				expectedBody: "[\"a\",\"b\",\"c\"]\n",
			},
			{
				name:         "post /version/endpoint with body",
				endpoint:     "version/endpoint",
				method:       http.MethodPost,
				body:         "no html & éscaping <",
				expectedBody: "\"no html & éscaping <\"\n",
			},
			{
				name:      "post /endpoint with body marshaling error",
				endpoint:  "version/endpoint",
				method:    http.MethodPost,
				body:      jsonMarshalError{},
				wantError: true,
			},
		} {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				req, err := c.newRequest(tc.method, tc.endpoint, tc.body)

				if tc.wantError {
					require.Error(t, err)
					return
				}

				require.NoError(t, err)
				require.Equal(t, tc.method, req.Method)
				require.Equal(t, baseURL+tc.endpoint, req.URL.String())
				if tc.expectedBody != "" {
					body, err := ioutil.ReadAll(req.Body)
					require.NoError(t, err)
					require.Equal(t, tc.expectedBody, string(body))
				}
			})
		}
	})

	t.Run("do", func(t *testing.T) {
		for _, tc := range []struct {
			name             string
			reqBody          interface{}
			expectedReqBody  string
			respBody         string
			expectedRespBody interface{}
			wantError        bool
			status           int
		}{
			{
				name: "no request nor response bodies",
			},
			{
				name:            "request body without response body",
				reqBody:         "string",
				expectedReqBody: "\"string\"\n",
			},
			{
				name:             "request and response body",
				reqBody:          "request",
				expectedReqBody:  "\"request\"\n",
				respBody:         "\"response\"\n",
				expectedRespBody: "response",
			},
			{
				name:             "no request body and response body",
				respBody:         "\"response\"\n",
				expectedRespBody: "response",
			},
			{
				name:      "bad response json",
				respBody:  "\"oops",
				wantError: true,
			},
			{
				name:      "error status code",
				status:    http.StatusUnprocessableEntity,
				wantError: true,
			},
			{
				name:   "ok status code",
				status: 200,
			},
		} {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.status != 0 {
						w.WriteHeader(tc.status)
					}
					if tc.expectedReqBody != "" {
						require.Equal(t, "application/json", r.Header.Get("Content-Type"))
						body, err := ioutil.ReadAll(r.Body)
						require.NoError(t, err)
						require.Equal(t, tc.expectedReqBody, string(body))
					}
					_, _ = w.Write([]byte(tc.respBody))
				}))
				defer srv.Close()

				c, err := NewClient(srv.Client(), srv.URL)

				req, err := c.newRequest("GET", "endpoint", tc.reqBody)
				require.NoError(t, err)

				var respBody interface{}
				err = c.do(context.Background(), req, &respBody)
				if tc.wantError {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tc.expectedRespBody, respBody)
			})
		}
	})

	t.Run("do with context", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(1 * time.Second)
		}))
		defer srv.Close()

		c, err := NewClient(srv.Client(), srv.URL)
		require.NoError(t, err)

		req, err := c.newRequest("PUT", "endpoint", nil)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
		defer cancel()
		err = c.do(ctx, req, nil)
		require.Error(t, err)
		require.True(t, errors.Is(err, context.DeadlineExceeded))
	})

	t.Run("do with context", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer srv.Close()

		c, err := NewClient(srv.Client(), srv.URL)
		require.NoError(t, err)

		req, err := c.newRequest("PUT", "endpoint", nil)
		require.NoError(t, err)

		err = c.do(nil, req, nil)
		require.Error(t, err)
	})
}

type jsonMarshalError struct{}

func (jsonMarshalError) UnmarshalJSON([]byte) error   { return errors.New("oops") }
func (jsonMarshalError) MarshalJSON() ([]byte, error) { return nil, errors.New("oops") }
