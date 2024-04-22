package actions

type (
	ActionEntry[T any] struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Parameters T      `json:"parameters"`
	}

	BlockActionParams struct {
		GRPCStatusCode *int   `json:"grpc_status_code,omitempty"`
		StatusCode     int    `json:"status_code"`
		Type           string `json:"type,omitempty"`
	}
	RedirectActionParams struct {
		Location   string `json:"location,omitempty"`
		StatusCode int    `json:"status_code"`
	}
)
