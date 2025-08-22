package streamprocessingoffload

import (
	"context"
	"fmt"
	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageHAProxyStreamProcessingOffload)
}

type HAProxySPOA struct {
	mp message_processor.MessageProcessor
}

type AppsecHAProxyConfig struct {
	Context              context.Context
	BlockingUnavailable  bool
	BodyParsingSizeLimit int
}

func NewHAProxySPOA(config AppsecHAProxyConfig) *HAProxySPOA {
	mp := message_processor.NewMessageProcessor(message_processor.MessageProcessorConfig{
		BlockingUnavailable:  config.BlockingUnavailable,
		BodyParsingSizeLimit: config.BodyParsingSizeLimit,
	}, instr)

	handler := &HAProxySPOA{
		mp: mp,
	}

	initRequestStateCache()

	return handler
}

// Handler processes SPOE requests from HAProxy
func (s *HAProxySPOA) Handler(req *request.Request) {
	instr.Logger().Debug("haproxy_spoa: handle request EngineID: '%s', StreamID: '%d', FrameID: '%d' with %d messages", req.EngineID, req.StreamID, req.FrameID, req.Messages.Len())

	// Process each message
	for i := 0; i < req.Messages.Len(); i++ {
		msg, err := req.Messages.GetByIndex(i)
		if err != nil {
			instr.Logger().Warn("haproxy_spoa: failed to get message at index %d: %v", i, err)
			continue
		}

		// Get current request state from cache or if nil it will be created by the request headers message
		reqState, _ := getCurrentRequest(msg)

		ctx := context.Background()
		reqState, mpAction, err := processMessage(s.mp, ctx, req, msg, reqState)
		if err != nil {
			instr.Logger().Error("haproxy_spoa: error processing message %s: %v", msg.Name, err)
			return
		}

		err = s.handleAction(mpAction, req, msg, reqState)
		if err != nil {
			instr.Logger().Error("haproxy_spoa: error processing message %s: %v", msg.Name, err)
			return
		}
	}
}

func processMessage(mp message_processor.MessageProcessor, ctx context.Context, req *request.Request, msg *message.Message, currentRequest *message_processor.RequestState) (*message_processor.RequestState, message_processor.Action, error) {
	instr.Logger().Debug("f: handling message: %s", msg.Name)

	switch msg.Name {
	case "http-request-headers-msg":
		return mp.OnRequestHeaders(ctx, &requestHeadersHAProxy{req: req, msg: msg})
	case "http-request-body-msg":
		if currentRequest == nil || !currentRequest.Ongoing {
			return nil, message_processor.Action{}, fmt.Errorf("received request body without request headers")
		}

		action, err := mp.OnRequestBody(&requestBodyHAProxy{msg: msg}, currentRequest)
		return currentRequest, action, err
	case "http-response-headers-msg":
		if currentRequest == nil || !currentRequest.Ongoing {
			return nil, message_processor.Action{}, fmt.Errorf("received response headers without request context")
		}

		action, err := mp.OnResponseHeaders(&responseHeadersHAProxy{msg: msg}, currentRequest)
		return currentRequest, action, err
	case "http-response-body-msg":
		if currentRequest == nil || !currentRequest.Ongoing {
			return nil, message_processor.Action{}, fmt.Errorf("received response body without request context")
		}

		action, err := mp.OnResponseBody(&responseBodyHAProxy{msg: msg}, currentRequest)
		return currentRequest, action, err
	default:
		return nil, message_processor.Action{}, fmt.Errorf("unknown message type: %s", msg.Name)
	}
}

func (s *HAProxySPOA) handleAction(action message_processor.Action, req *request.Request, msg *message.Message, reqState *message_processor.RequestState) error {
	switch action.Type {
	case message_processor.ActionTypeContinue:
		if action.Response == nil {
			return nil
		}

		if data := action.Response.(*message_processor.HeadersResponseData); data != nil {
			return setHeadersResponseData(data, req, msg, reqState)
		}

		// Could happen if a new response type with data is implemented, and we forget to handle it here.
		// However, at the moment, we only have HeadersResponseData as a response type for ActionTypeContinue
		return fmt.Errorf("unknown action data type: %T for ActionTypeContinue", action.Response)
	case message_processor.ActionTypeBlock:
		data := action.Response.(*message_processor.BlockResponseData)
		_ = deleteCurrentRequest(reqState.Span.Context().SpanID())
		return setBlockResponseData(data, req)
	case message_processor.ActionTypeFinish:
		_ = deleteCurrentRequest(reqState.Span.Context().SpanID())
		return nil
	}

	return fmt.Errorf("unknown action type: %T", action.Type)
}
