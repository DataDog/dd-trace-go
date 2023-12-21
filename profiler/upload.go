// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// maxRetries specifies the maximum number of retries to have when an error occurs.
const maxRetries = 2

var errOldAgent = errors.New("Datadog Agent is not accepting profiles. Agent-based profiling deployments " +
	"require Datadog Agent >= 7.20")

// upload tries to upload a batch of profiles. It has retry and backoff mechanisms.
func (p *profiler) upload(bat batch) error {
	statsd := p.cfg.statsd
	var err error
	for i := 0; i < maxRetries; i++ {
		select {
		case <-p.exit:
			return nil
		default:
		}

		err = p.doRequest(bat)
		if rerr, ok := err.(*retriableError); ok {
			statsd.Count("datadog.profiling.go.upload_retry", 1, nil, 1)
			wait := time.Duration(rand.Int63n(p.cfg.period.Nanoseconds())) * time.Nanosecond
			log.Error("Uploading profile failed: %v. Trying again in %s...", rerr, wait)
			p.interruptibleSleep(wait)
			continue
		}
		if err != nil {
			statsd.Count("datadog.profiling.go.upload_error", 1, nil, 1)
		} else {
			statsd.Count("datadog.profiling.go.upload_success", 1, nil, 1)
			var b int64
			for _, p := range bat.profiles {
				b += int64(len(p.data))
			}
			statsd.Count("datadog.profiling.go.uploaded_profile_bytes", b, nil, 1)
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
	tags := append(p.cfg.tags.Slice(),
		fmt.Sprintf("service:%s", p.cfg.service),
		// The profile_seq tag can be used to identify the first profile
		// uploaded by a given runtime-id, identify missing profiles, etc.. See
		// PROF-5612 (internal) for more details.
		fmt.Sprintf("profile_seq:%d", bat.seq),
	)
	tags = append(tags, bat.extraTags...)
	// If the user did not configure an "env" in the client, we should omit
	// the tag so that the agent has a chance to supply a default tag.
	// Otherwise, the tag supplied by the client will have priority.
	if p.cfg.env != "" {
		tags = append(tags, fmt.Sprintf("env:%s", p.cfg.env))
	}
	contentType, body, err := encode(bat, tags)
	if err != nil {
		return err
	}
	funcExit := make(chan struct{})
	defer close(funcExit)
	// uploadTimeout is guaranteed to be >= 0, see newProfiler.
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.uploadTimeout)
	go func() {
		select {
		case <-p.exit:
		case <-funcExit:
		}
		cancel()
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", p.cfg.targetURL, body)
	if err != nil {
		return err
	}
	if p.cfg.apiKey != "" {
		req.Header.Set("DD-API-KEY", p.cfg.apiKey)
	}
	if containerID != "" {
		req.Header.Set("Datadog-Container-ID", containerID)
	}
	if entityID != "" {
		req.Header.Set("Datadog-Entity-ID", entityID)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := p.cfg.httpClient.Do(req)
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
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		// Success!
		return nil
	}
	return errors.New(resp.Status)
}

type uploadEvent struct {
	Start            string            `json:"start"`
	End              string            `json:"end"`
	Attachments      []string          `json:"attachments"`
	Tags             string            `json:"tags_profiler"`
	Family           string            `json:"family"`
	Version          string            `json:"version"`
	EndpointCounts   map[string]uint64 `json:"endpoint_counts,omitempty"`
	CustomAttributes []string          `json:"custom_attributes,omitempty"`
}

// encode encodes the profile as a multipart mime request.
func encode(bat batch, tags []string) (contentType string, body io.Reader, err error) {
	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)

	if bat.host != "" {
		tags = append(tags, fmt.Sprintf("host:%s", bat.host))
	}
	tags = append(tags, "runtime:go")

	event := &uploadEvent{
		Version:          "4",
		Family:           "go",
		Start:            bat.start.Format(time.RFC3339Nano),
		End:              bat.end.Format(time.RFC3339Nano),
		Tags:             strings.Join(tags, ","),
		EndpointCounts:   bat.endpointCounts,
		CustomAttributes: bat.customAttributes,
	}

	for _, p := range bat.profiles {
		event.Attachments = append(event.Attachments, p.name)
		f, err := mw.CreateFormFile(p.name, p.name)
		if err != nil {
			return "", nil, err
		}
		if _, err := f.Write(p.data); err != nil {
			return "", nil, err
		}
	}

	f, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="event"; filename="event.json"`},
		"Content-Type":        []string{"application/json"},
	})
	if err != nil {
		return "", nil, err
	}
	if err := json.NewEncoder(f).Encode(event); err != nil {
		return "", nil, err
	}

	if err := mw.Close(); err != nil {
		return "", nil, err
	}
	return mw.FormDataContentType(), &buf, nil
}
