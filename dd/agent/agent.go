package agent

import (
	"fmt"
	"io"
	"net/http"
)

type Agent struct {
	conf cfg

	// cache of the agent's features
	features AgentFeatures
}

func New(opts ...Option) *Agent {
	return &Agent{
		conf: newConfig(opts...),
	}
}

func (a *Agent) SubmitStats(p io.Reader) error {
	req, err := http.NewRequest("POST", a.conf.statsURL, p)
	if err != nil {
		return err
	}
	resp, err := a.conf.client.Do(req)
	if err != nil {
		return err
	}
	if code := resp.StatusCode; code >= 400 {
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := resp.Body.Read(msg)
		resp.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return fmt.Errorf("%s", txt)
	}
	return nil
}

func (a *Agent) SubmitTraces(p io.Reader, headers map[string]string) (body io.ReadCloser, err error) {
	req, err := http.NewRequest("POST", a.conf.traceURL, p)
	if err != nil {
		return nil, fmt.Errorf("cannot create http request: %v", err)
	}
	for header, value := range a.conf.traceHeaders {
		req.Header.Set(header, value)
	}

	//req.Header.Set(traceCountHeader, strconv.Itoa(p.itemCount()))
	//req.Header.Set("Content-Length", strconv.Itoa(p.size()))
	//req.Header.Set(headerComputedTopLevel, "yes")
	//if t, ok := traceinternal.GetGlobalTracer().(*tracer); ok {
	//	if t.config.canComputeStats() {
	//		req.Header.Set("Datadog-Client-Computed-Stats", "yes")
	//	}
	//	droppedTraces := int(atomic.SwapUint32(&t.droppedP0Traces, 0))
	//	partialTraces := int(atomic.SwapUint32(&t.partialTraces, 0))
	//	droppedSpans := int(atomic.SwapUint32(&t.droppedP0Spans, 0))
	//	if stats := t.statsd; stats != nil {
	//		stats.Count("datadog.tracer.dropped_p0_traces", int64(droppedTraces),
	//			[]string{fmt.Sprintf("partial:%s", strconv.FormatBool(partialTraces > 0))}, 1)
	//		stats.Count("datadog.tracer.dropped_p0_spans", int64(droppedSpans), nil, 1)
	//	}
	//	req.Header.Set("Datadog-Client-Dropped-P0-Traces", strconv.Itoa(droppedTraces))
	//	req.Header.Set("Datadog-Client-Dropped-P0-Spans", strconv.Itoa(droppedSpans))
	//}

	response, err := a.conf.client.Do(req)
	if err != nil {
		return nil, err
	}
	if code := response.StatusCode; code >= 400 {
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := response.Body.Read(msg)
		response.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return nil, fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return nil, fmt.Errorf("%s", txt)
	}
	return response.Body, nil
}

func (a *Agent) Do(req *http.Request) (*http.Response, error) {
	return a.conf.client.Do(req)
}
