package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/http-wasm/http-wasm-guest-tinygo/handler"
	"github.com/http-wasm/http-wasm-guest-tinygo/handler/api"

	// This configures net.Dial for WASI sockets
	_ "github.com/stealthrocket/net/http"
	wasip1 "github.com/stealthrocket/net/wasip1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/puzpuzpuz/xsync/v3"
)

type TraefikPluginConfig struct {
	ExternalProcessingHost string `json:"external_processing_host"`
	ExternalProcessingPort string `json:"external_processing_port"`
	Timeout                string `json:"timeout"`
}

type ASMExternalProcessingConfig struct {
	Timeout int
}

type StreamContext struct {
	stream   extproc.ExternalProcessor_ProcessClient
	channel  chan *ResponseChannel
	ctx      context.Context
	cancelFn context.CancelFunc
}

type ResponseChannel struct {
	Request  api.Request
	Response api.Response
	IsError  bool
}

type ASMExternalProcessing struct {
	client extproc.ExternalProcessorClient
	config *ASMExternalProcessingConfig

	idToStream *xsync.MapOf[uint32, *StreamContext]
}

func init() {
	// Because there is no file mounted in the plugin by default, we configure insecureSkipVerify to avoid having to load rootCas
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec

	var config TraefikPluginConfig
	err := json.Unmarshal(handler.Host.GetConfig(), &config)
	if err != nil {
		handler.Host.Log(api.LogLevelError, "Failed to configure the ASM Datadog Plugin. No protection running. Error: "+err.Error())
		return
	}

	plugin, err := NewASMExternalProcessing(&config)
	if err != nil {
		handler.Host.Log(api.LogLevelError, "Failed to configure the ASM Datadog Plugin. No protection running. Error: "+err.Error())
		return
	}

	handler.HandleRequestFn = plugin.handleRequest
	handler.HandleResponseFn = plugin.handleResponse
	handler.Host.EnableFeatures(api.FeatureBufferRequest | api.FeatureBufferResponse)
}

func NewASMExternalProcessing(config *TraefikPluginConfig) (*ASMExternalProcessing, error) {
	plugin := &ASMExternalProcessing{}

	// Config part
	timeout, err := strconv.Atoi(config.Timeout)
	if err != nil {
		handler.Host.Log(api.LogLevelDebug, "Failed to convert timeout to int: "+err.Error())
		return nil, err
	}

	plugin.config = &ASMExternalProcessingConfig{
		Timeout: timeout,
	}

	// GRPC part
	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:%s", config.ExternalProcessingHost, config.ExternalProcessingPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return wasip1.DialContext(ctx, "tcp", addr)
		}),
	)
	if err != nil {
		return nil, err
	}

	plugin.client = extproc.NewExternalProcessorClient(conn)
	plugin.idToStream = xsync.NewMapOf[uint32, *StreamContext]()

	return plugin, nil
}

func (plugin *ASMExternalProcessing) handleRequest(req api.Request, resp api.Response) (next bool, reqCtx uint32) {
	handler.Host.Log(api.LogLevelDebug, "Processing request with ExtProc")

	// Create a context with timeout that will be used for the entire request/response cycle
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plugin.config.Timeout)*time.Millisecond)

	// Create a stream for communication with ExtProc server
	stream, err := plugin.client.Process(ctx)
	if err != nil {
		cancel()
		handler.Host.Log(api.LogLevelDebug, "Failed to create ExtProc stream: "+err.Error())
		return false, 0 // Fail close // Todo: Write 500 Error to the response
	}

	blocked, err := handleRequestHeaders(stream, req, resp)
	if err != nil {
		cancel()
		handler.Host.Log(api.LogLevelDebug, "Failed to handle request headers: "+err.Error())
		return false, 0 // Fail close // Todo: Write 500 Error to the reponse
	}

	if blocked {
		cancel()
		handler.Host.Log(api.LogLevelDebug, "Request blocked")
		return false, 0 // Fail close
	}

	// Create a channel for the response
	streamChan := make(chan *ResponseChannel)

	// Generate a unique ID for this request/response pair
	id := generateID()

	// Store the stream, context, and cancel function in the map
	streamCtx := &StreamContext{
		stream:   stream,
		channel:  streamChan,
		ctx:      ctx,
		cancelFn: cancel,
	}
	plugin.idToStream.Store(id, streamCtx)

	go func() {
		defer func() {
			handler.Host.Log(api.LogLevelDebug, "Ending goroutine")
			plugin.idToStream.Delete(id)
			stream.CloseSend()
			close(streamChan)
			cancel()
		}()

		select {
		case response := <-streamChan:

			blocked, err := handleResponseHeaders(stream, response.Request, response.Response)
			if err != nil {
				handler.Host.Log(api.LogLevelDebug, "Failed to handle response headers: "+err.Error())
				return
			}

			if blocked {
				handler.Host.Log(api.LogLevelDebug, "Response blocked")
				return
			}

			return

		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				handler.Host.Log(api.LogLevelDebug, "Timeout while waiting for response from ExtProc")
				return
			}
			handler.Host.Log(api.LogLevelDebug, "Context error: "+ctx.Err().Error())
			return
		}
	}()

	return true, id
}

func (plugin *ASMExternalProcessing) handleResponse(reqCtx uint32, req api.Request, resp api.Response, isError bool) {
	handler.Host.Log(api.LogLevelDebug, "Processing response with ExtProc")

	streamCtx, ok := plugin.idToStream.Load(reqCtx)
	if !ok {
		handler.Host.Log(api.LogLevelDebug, "No stream found for request context: "+strconv.FormatUint(uint64(reqCtx), 10))
		return
	}

	if isError {
		// TODO: Handle the error
		handler.Host.Log(api.LogLevelDebug, "Error received from middleware")
		streamCtx.cancelFn()
		return
	}

	streamCtx.channel <- &ResponseChannel{
		Request:  req,
		Response: resp,
		IsError:  isError,
	}

	// Wait for context to be done (reponse processed)
	<-streamCtx.ctx.Done()

	handler.Host.Log(api.LogLevelDebug, "Finished processing response !")

	return
}

func main() {
	// This is needed for TinyGo to compile correctly
	// The actual execution starts from the init function
}
