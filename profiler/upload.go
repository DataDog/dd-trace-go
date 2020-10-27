// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// maxRetries specifies the maximum number of retries to have when an error occurs.
const maxRetries = 2

var errOldAgent = errors.New("Datadog Agent is not accepting profiles. Agent-based profiling deployments " +
	"require Datadog Agent >= 7.20")

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// backoffDuration calculates the backoff duration given an attempt number and max duration
func backoffDuration(attempt int, max time.Duration) time.Duration {
	// https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
	if attempt == 0 {
		return 0
	}
	maxPow := float64(max / 100 * time.Millisecond)
	pow := math.Min(math.Pow(2, float64(attempt)), maxPow)
	ns := int64(float64(100*time.Millisecond) * pow)
	return time.Duration(rand.Int63n(ns))
}

// upload tries to upload a batch of profiles. It has retry and backoff mechanisms.
func (p *profiler) upload(bat batch) error {
	statsd := p.cfg.statsd
	var err error
	for i := 0; i < maxRetries; i++ {
		err = p.doRequest(bat)
		if rerr, ok := err.(*retriableError); ok {
			statsd.Count("datadog.profiler.go.upload_retry", 1, nil, 1)
			wait := backoffDuration(i+1, p.cfg.cpuDuration)
			log.Error("Uploading profile failed: %v. Trying again in %s...", rerr, wait)
			time.Sleep(wait)
			continue
		}
		if err != nil {
			statsd.Count("datadog.profiler.go.upload_error", 1, nil, 1)
		} else {
			statsd.Count("datadog.profiler.go.upload_success", 1, nil, 1)
			var b int64
			for _, p := range bat.profiles {
				b += int64(len(p.data))
			}
			statsd.Count("datadog.profiler.go.uploaded_profile_bytes", b, nil, 1)
		}
		return err
	}
	return fmt.Errorf("failed after %d retries, last error was: %v", maxRetries, err)
}

// retriableError is an error returned by the server which may be retried at a later time.
type retriableError struct{ err error }

// Error implements error.
func (e retriableError) Error() string { return e.err.Error() }

// doRequest makes an HTTP POST request to the Datadog Profiling API with the
// given profile.
func (p *profiler) doRequest(bat batch) error {
	tags := append(p.cfg.tags,
		fmt.Sprintf("service:%s", p.cfg.service),
		fmt.Sprintf("env:%s", p.cfg.env),
	)
	contentType, body, err := encode(bat, tags)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", p.cfg.targetURL, body)
	if err != nil {
		return err
	}
	if p.cfg.apiKey != "" {
		req.Header.Set("DD-API-KEY", p.cfg.apiKey)
	}
	if containerID != "" {
		req.Header.Set("Datadog-Container-ID", containerID)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := httpClient.Do(req)
	if err != nil {
		return &retriableError{err}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 5 {
		// 5xx can be retried
		return &retriableError{errors.New(resp.Status)}
	}
	if resp.StatusCode == 404 && p.cfg.targetURL == p.cfg.agentURL {
		// 404 from the agent means we have an old agent version without profiling endpoint
		return errOldAgent
	}
	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}
	return nil
}

// encode encodes the profile as a multipart mime request.
func encode(bat batch, tags []string) (contentType string, body io.Reader, err error) {
	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)
	// write all of the profile metadata (including some useless ones)
	// with a small helper function that makes error tracking less verbose.
	writeField := func(k, v string) {
		if err == nil {
			err = mw.WriteField(k, v)
		}
	}
	writeField("format", "pprof")
	writeField("runtime", "go")
	writeField("recording-start", bat.start.Format(time.RFC3339))
	writeField("recording-end", bat.end.Format(time.RFC3339))
	if bat.host != "" {
		writeField("tags[]", fmt.Sprintf("host:%s", bat.host))
	}
	writeField("tags[]", "runtime:go")
	for _, tag := range tags {
		writeField("tags[]", tag)
	}
	for i, p := range bat.profiles {
		writeField(fmt.Sprintf("types[%d]", i), strings.Join(p.types, ","))
	}
	if err != nil {
		return "", nil, err
	}
	for i, p := range bat.profiles {
		formFile, err := mw.CreateFormFile(fmt.Sprintf("data[%d]", i), "pprof-data")
		if err != nil {
			return "", nil, err
		}
		if _, err := formFile.Write(p.data); err != nil {
			return "", nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return "", nil, err
	}
	return mw.FormDataContentType(), &buf, nil
}
