package message_processor

import "net/http"

type action struct {
	actionType ActionType
	response   any
}

func (a *action) Type() ActionType {
	return a.actionType
}

func (a *action) Response() any {
	return a.response
}

func newContinueAction() Action {
	return &action{actionType: ActionTypeContinue}
}

func newContinueAndReplaceAction(mutations http.Header, requestBody bool) Action {
	return &action{
		actionType: ActionTypeContinue,
		response:   &HeadersResponseData{HeaderMutation: mutations, RequestBody: requestBody},
	}
}

func newBlockAction(writer *fakeResponseWriter) Action {
	return &action{
		actionType: ActionTypeBlock,
		response: &BlockResponseData{
			StatusCode: int(writer.status),
			Headers:    writer.headers,
			Body:       writer.body,
		},
	}
}

func newFinishAction() Action {
	return &action{
		actionType: ActionTypeFinish,
	}
}

// HeadersResponseData is the data for a headers response
type HeadersResponseData struct {
	HeaderMutation http.Header
	RequestBody    bool
}

// BlockResponseData is the data for an immediate response
type BlockResponseData struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}
