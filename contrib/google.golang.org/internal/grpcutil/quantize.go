package grpcutil

import "strings"

// QuantizeResource will quantize the given method information and attempt to return the parsed
// service and method. If a service is passed, it will override any service extracted from the
// method information.
func QuantizeResource(service, method string) (string, string) {
	switch {
	case strings.HasPrefix(method, "/pb."):
		method = method[4:]
	case strings.HasPrefix(method, "/"):
		method = method[1:]
	}
	if idx := strings.LastIndexByte(method, '/'); idx > 0 {
		if service == "" {
			// user has not chosen a custom service name
			service = method[:idx]
		}
		method = method[idx+1:]
	}
	return service, method
}
