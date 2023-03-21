package namingschema

type httpOutboundOperationNameSchema struct{}

func NewHTTPOutboundOperationNameSchema() Schema {
	return New(&httpOutboundOperationNameSchema{})
}

func (h *httpOutboundOperationNameSchema) V0() string {
	return "http.request"
}

func (h *httpOutboundOperationNameSchema) V1() string {
	return "http.client.request"
}

type httpInboundOperationNameSchema struct{}

func NewHTTPInboundOperationNameSchema() Schema {
	return New(&httpInboundOperationNameSchema{})
}

func (h *httpInboundOperationNameSchema) V0() string {
	return "http.request"
}

func (h *httpInboundOperationNameSchema) V1() string {
	return "http.server.request"
}
