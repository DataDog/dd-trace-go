// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Client is the HTTP client to use to communicate with the intake API via the agent API.
type client struct {
	// Logger should be set to obtain debugging logs in debug level to see the HTTP requests and their responses.
	Logger  debugLogger
	client  *http.Client
	baseURL *url.URL
}

// debugLogger interface of the debug-level logger.
type debugLogger interface {
	Debug(format string, v ...interface{})
}

// newClient returns a new intake client using the given HTTP client and base-URL.
func newClient(httpClient *http.Client, baseURL string) (*client, error) {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	return &client{
		client:  httpClient,
		baseURL: u,
	}, nil
}

// sendBatch sends the batch.
func (c *client) sendBatch(ctx context.Context, b eventBatch) error {
	r, err := c.newRequest("POST", "appsec/proxy/api/v2/appsecevts", b)
	if err != nil {
		return err
	}
	return c.do(ctx, r, nil)
}

func (c *client) newRequest(method, urlStr string, reqBody interface{}) (*http.Request, error) {
	u, err := c.baseURL.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	var buf io.ReadWriter
	if reqBody != nil {
		buf = &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(reqBody); err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest(method, u.String(), buf)
	if err != nil {
		return nil, err
	}

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *client) do(ctx context.Context, req *http.Request, respBody interface{}) error {
	if ctx == nil {
		return errors.New("context must be non-nil")
	}

	req = req.WithContext(ctx)

	c.debug("sending request\n%s\n", (*httpRequestStringer)(req))

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// Drain the body and close it in order to make the underlying connection
		// available again in the pool
		_, _ = io.Copy(ioutil.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	c.debug("received response\n%s\n", (*httpResponseStringer)(resp))

	err = checkResponse(resp)
	if err != nil {
		return err
	}

	if respBody != nil {
		decErr := json.NewDecoder(resp.Body).Decode(respBody)
		if decErr != nil && decErr != io.EOF {
			return decErr
		}
	}

	return nil
}

func (c *client) debug(fmt string, args ...interface{}) {
	if c.Logger == nil {
		return
	}
	c.Logger.Debug(fmt, args...)
}

type (
	httpRequestStringer  http.Request
	httpResponseStringer http.Response
)

func (r *httpRequestStringer) String() string {
	dump, _ := httputil.DumpRequestOut((*http.Request)(r), true)
	return string(dump)
}

func (r *httpResponseStringer) String() string {
	dump, _ := httputil.DumpResponse((*http.Response)(r), true)
	return string(dump)
}

// Client error types.
type (
	// APIError is the generic request error returned when the request status
	// code is unknown.
	APIError struct {
		Response *http.Response
	}
	// AuthTokenError is a request error returned when the request could not be
	// authenticated.
	AuthTokenError APIError
	// InvalidSignalError is a request error returned when one or more signal(s)
	// sent are invalid.
	InvalidSignalError APIError
)

// Error return the error string representation.
func (e APIError) Error() string {
	return fmt.Sprintf("api error: response with status code %s", e.Response.Status)
}

// Error return the error string representation.
func (e AuthTokenError) Error() string {
	return "api error: access token is missing or invalid"
}

// Error return the error string representation.
func (e InvalidSignalError) Error() string {
	return "api error: one of the provided events is invalid"
}

func checkResponse(r *http.Response) error {
	if c := r.StatusCode; 200 <= c && c <= 299 {
		return nil
	}
	errorResponse := APIError{Response: r}
	switch r.StatusCode {
	case http.StatusUnauthorized:
		return AuthTokenError(errorResponse)
	case http.StatusUnprocessableEntity:
		return InvalidSignalError(errorResponse)
	default:
		return errorResponse
	}
}
