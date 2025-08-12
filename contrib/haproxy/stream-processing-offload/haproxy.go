package streamprocessingoffload

import (
	"context"
	"fmt"
	"log"

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
	log.Printf("handle request EngineID: '%s', StreamID: '%d', FrameID: '%d' with %d messages",
		req.EngineID, req.StreamID, req.FrameID, req.Messages.Len())

	mp := message_processor.NewMessageProcessor(
		message_processor.MessageProcessorConfig{
			BlockingUnavailable:  false,
			BodyParsingSizeLimit: 1024,
		},
		instr,
	)

	// Process each message
	for i := 0; i < req.Messages.Len(); i++ {
		msg, err := req.Messages.GetByIndex(i)
		if err != nil {
			log.Printf("Failed to get message at index %d: %v", i, err)
			continue
		}

		ctx := context.Background()
		mpAction, reqState, err := processMessage(mp, ctx, req, msg)
		if err != nil {
			log.Printf("Error processing message %s: %v", msg.Name, err)
			return
		}

		err = s.handleAction(mpAction, req, &reqState)
		if err != nil {
			log.Printf("Error handling action for message %s: %v", msg.Name, err)
			return
		}
	}
}

func processMessage(mp message_processor.MessageProcessor, ctx context.Context, req *request.Request, msg *message.Message) (message_processor.Action, message_processor.RequestState, error) {
	log.Printf("Handling message: %s", msg.Name)

	switch msg.Name {
	case "http-request-headers-msg":
		var (
			mpAction       message_processor.Action
			err            error
			currentRequest message_processor.RequestState
		)
		currentRequest, mpAction, err = mp.OnRequestHeaders(ctx, &requestHeadersHAProxy{req: req, msg: msg})
		return mpAction, currentRequest, err
	case "http-request-body-msg":
		currentRequest, err := getCurrentRequest(msg)
		if err != nil {
			return message_processor.Action{}, message_processor.RequestState{}, err
		}

		var action message_processor.Action
		action, err = mp.OnRequestBody(&requestBodyHAProxy{msg: msg}, currentRequest)
		return action, currentRequest, err
	case "http-response-headers-msg":
		currentRequest, err := getCurrentRequest(msg)
		if err != nil {
			return message_processor.Action{}, message_processor.RequestState{}, err
		}

		var action message_processor.Action
		action, err = mp.OnResponseHeaders(&responseHeadersHAProxy{msg: msg}, currentRequest)
		return action, currentRequest, err
	case "http-response-body-msg":
		currentRequest, err := getCurrentRequest(msg)
		if err != nil {
			return message_processor.Action{}, message_processor.RequestState{}, err
		}

		var action message_processor.Action
		action, err = mp.OnResponseBody(&responseBodyHAProxy{msg: msg}, currentRequest)
		return action, currentRequest, err
	default:
		return message_processor.Action{}, message_processor.RequestState{}, fmt.Errorf("unknown message type: %s", msg.Name)
	}
}

func (s *HAProxySPOA) handleAction(action message_processor.Action, req *request.Request, reqState *message_processor.RequestState) error {
	switch action.Type {
	case message_processor.ActionTypeContinue:
		if action.Response == nil {
			return nil
		}

		if data := action.Response.(*message_processor.HeadersResponseData); data != nil {
			// Set the headers in the request
			// setHeadersResponseData(data)
			return nil
		}

		// Could happen if a new response type with data is implemented, and we forget to handle it here.
		// However, at the moment, we only have HeadersResponseData as a response type for ActionTypeContinue
		return fmt.Errorf("unknown action data type: %T for ActionTypeContinue", action.Response)
	case message_processor.ActionTypeBlock:
		data := action.Response.(*message_processor.BlockResponseData)
		setBlockResponseData(data, req)
		_ = deleteCurrentRequest(reqState.Span.Context().SpanID())
		return nil
	case message_processor.ActionTypeFinish:
		// Remove the current request from the cache
		_ = deleteCurrentRequest(reqState.Span.Context().SpanID())
	}

	return fmt.Errorf("unknown action type: %T", action.Type)
}
