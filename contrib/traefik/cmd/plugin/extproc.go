package plugin

import (
	"errors"
	"strconv"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/http-wasm/http-wasm-guest-tinygo/handler"
	"github.com/http-wasm/http-wasm-guest-tinygo/handler/api"
)

func handleRequestHeaders(stream extproc.ExternalProcessor_ProcessClient, req api.Request, resp api.Response) (bool, error) {
	handler.Host.Log(api.LogLevelDebug, "Processing request headers")

	// https://doc.traefik.io/traefik/getting-started/faq/#what-are-the-forwarded-headers-when-proxying-http-requests
	scheme, ok := req.Headers().Get("X-Forwarded-Proto")
	if !ok {
		return false, errors.New("failed to get X-Forwarded-Proto")
	}

	path := req.GetURI()
	method := req.GetMethod()

	headers := req.Headers()
	headerValues := make([]*core.HeaderValue, 0, len(headers.Names())+4)

	headerValues = append(headerValues, &core.HeaderValue{
		Key:      ":method",
		RawValue: []byte(method),
	})
	headerValues = append(headerValues, &core.HeaderValue{
		Key:      ":scheme",
		RawValue: []byte(scheme),
	})
	headerValues = append(headerValues, &core.HeaderValue{
		Key:      ":path",
		RawValue: []byte(path),
	})
	headerValues = append(headerValues, &core.HeaderValue{
		Key:      ":authority",
		RawValue: []byte("dd-asm-traefik"),
	})

	for _, header := range headers.Names() {
		value, ok := headers.Get(header)
		if ok {
			headerValues = append(headerValues, &core.HeaderValue{
				Key:      header,
				RawValue: []byte(value),
			})
		}
	}

	// Build the ExtProc request
	procReq := &extproc.ProcessingRequest{
		Request: &extproc.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extproc.HttpHeaders{
				Headers: &core.HeaderMap{
					Headers: headerValues,
				},
			},
		},
	}

	// Send the request to ExtProc server
	if err := stream.Send(procReq); err != nil {
		return false, err
	}

	// Receive the response
	answer, err := stream.Recv()
	if err != nil {
		return false, err
	}

	// Handle the response
	switch r := answer.GetResponse().(type) {
	case *extproc.ProcessingResponse_ImmediateResponse:
		return true, handleBlocking(resp, r.ImmediateResponse)
	case *extproc.ProcessingResponse_RequestHeaders:
		return false, handleHeadersMutation(resp, r.RequestHeaders.GetResponse().GetHeaderMutation())
	default:
		return false, errors.New("unknown response request headers type")
	}
}

func handleResponseHeaders(stream extproc.ExternalProcessor_ProcessClient, req api.Request, resp api.Response) (bool, error) {
	handler.Host.Log(api.LogLevelDebug, "Processing response headers")
	statusCode := resp.GetStatusCode()

	headers := resp.Headers()
	headerValues := make([]*core.HeaderValue, 0, len(headers.Names())+1)

	headerValues = append(headerValues, &core.HeaderValue{
		Key:      ":status",
		RawValue: []byte(strconv.FormatUint(uint64(statusCode), 10)),
	})

	for _, header := range headers.Names() {
		value, ok := headers.Get(header)
		if ok {
			headerValues = append(headerValues, &core.HeaderValue{
				Key:      header,
				RawValue: []byte(value),
			})
		}
	}

	procReq := &extproc.ProcessingRequest{
		Request: &extproc.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &extproc.HttpHeaders{
				Headers: &core.HeaderMap{
					Headers: headerValues,
				},
			},
		},
	}

	// Send the request to ExtProc server
	if err := stream.Send(procReq); err != nil {
		handler.Host.Log(api.LogLevelDebug, "Failed to send response headers: "+err.Error())
		return false, err
	}

	// Receive the response
	answer, err := stream.Recv()
	if err != nil {
		handler.Host.Log(api.LogLevelDebug, "Failed to receive response for response headers: "+err.Error())
		return false, err
	}

	// Handle the response
	switch answer.GetResponse().(type) {
	case *extproc.ProcessingResponse_ImmediateResponse:
		return true, handleBlocking(resp, answer.GetImmediateResponse())
	case *extproc.ProcessingResponse_ResponseHeaders:
		return false, handleHeadersMutation(resp, answer.GetResponseHeaders().GetResponse().GetHeaderMutation())
	default:
		return false, errors.New("unknown response response headers type")
	}
}

func handleHeadersMutation(resp api.Response, headersMutation *extproc.HeaderMutation) error {
	if headersMutation == nil {
		return nil
	}

	handler.Host.Log(api.LogLevelDebug, "Processing request headers mutation with ExtProc")

	for _, removedHeader := range headersMutation.GetRemoveHeaders() {
		handler.Host.Log(api.LogLevelDebug, "Removing header: "+removedHeader)
		resp.Headers().Remove(removedHeader)
	}

	for _, addedHeader := range headersMutation.GetSetHeaders() {
		handler.Host.Log(api.LogLevelDebug, "Adding header: "+addedHeader.Header.Key+" "+string(addedHeader.Header.RawValue))
		resp.Headers().Add(addedHeader.Header.Key, string(addedHeader.Header.RawValue))
	}

	return nil
}

func handleBodyMutation(resp api.Response, body []byte) {
	handler.Host.Log(api.LogLevelDebug, "Processing body mutation with ExtProc")

	resp.Body().Write(body)
	resp.Headers().Add("Content-Length", strconv.Itoa(len(body)))
}

func handleBlocking(resp api.Response, immediateResponse *extproc.ImmediateResponse) error {
	if immediateResponse == nil {
		return nil
	}

	handler.Host.Log(api.LogLevelDebug, "Blocking request with ExtProc")

	statusCode := immediateResponse.GetStatus().GetCode()
	headersMutation := immediateResponse.GetHeaders()
	bodyMutation := immediateResponse.GetBody()

	if statusCode != 0 {
		resp.SetStatusCode(uint32(statusCode))
	}

	if headersMutation != nil {
		err := handleHeadersMutation(resp, headersMutation)
		if err != nil {
			return err
		}
	}

	if bodyMutation != nil {
		handleBodyMutation(resp, bodyMutation)
	}

	return nil
}
