// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"iter"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// The protocol names of both major versions of connect.
const (
	protocolConnect = "connect"
	protocolGRPC    = "grpc"
	protocolGRPCWeb = "grpcweb"
)

// Protocol holds every protocol-dependent tag decision.
type Protocol struct {
	system              string
	grpcStatus          bool // numeric status tag always set; rpc.grpc.full_method set
	statusCodeTag       string
	requestTag          string
	statusDetailsPrefix string
	notModifiedEligible bool
}

var (
	connectProtocol = Protocol{
		system:              ext.RPCSystemConnectRPC,
		statusCodeTag:       tagConnectErrorCode,
		requestTag:          tagConnectRequest,
		statusDetailsPrefix: "connect.status_details.",
		notModifiedEligible: true,
	}
	// inProcessProtocol is tagged like Connect, but a not-modified error is not a 304: a transport
	// without a wire protocol, such as connect.v2's connectinprocess, keeps the server's
	// not-modified error.
	inProcessProtocol = func() Protocol {
		p := connectProtocol
		p.notModifiedEligible = false
		return p
	}()
	// unknownProtocol sets none of the tags that name a protocol, as the call may be over any. The
	// request and error detail tags keep their Connect names.
	unknownProtocol = Protocol{requestTag: tagConnectRequest, statusDetailsPrefix: "connect.status_details."}
	grpcProtocol    = Protocol{
		system:              ext.RPCSystemGRPC,
		grpcStatus:          true,
		statusCodeTag:       tagGRPCStatusCode,
		requestTag:          tagGRPCRequest,
		statusDetailsPrefix: "grpc.status_details.",
	}
)

// ProtocolFor returns the protocol that a transport reported for a call as name: gRPC and gRPC-Web
// have the gRPC tags, and Connect the Connect tags. Any other name, including an empty one, is an
// unknown protocol: its calls get none of the tags that name a protocol (rpc.system,
// rpc.grpc.full_method and the status code tag), and a not-modified error is not a 304.
func ProtocolFor(name string) *Protocol {
	switch name {
	case protocolGRPC, protocolGRPCWeb:
		return &grpcProtocol
	case protocolConnect:
		return &connectProtocol
	default:
		return &unknownProtocol
	}
}

// InProcessProtocol returns the protocol of a call that a transport carried without a wire
// protocol, such as connect.v2's connectinprocess: it has the Connect tags, but a not-modified
// error is not a 304.
func InProcessProtocol() *Protocol {
	return &inProcessProtocol
}

// addProcedureTags adds the resource, rpc.service, rpc.method and method kind tags of spec, and
// rpc.method_original if it is unrecognized.
func addProcedureTags(tags map[string]any, spec Spec) {
	service, method := parseProcedure(spec.Procedure)
	tags[ext.RPCService] = service
	tags[tagMethodKind] = methodKind(spec.StreamType)
	if spec.Unrecognized {
		tags[ext.ResourceName] = otherMethod
		tags[ext.RPCMethod] = otherMethod
		tags[tagRPCMethodOriginal] = method
		return
	}
	tags[ext.ResourceName] = spec.Procedure
	tags[ext.RPCMethod] = method
}

// addProtocolTags adds the tags of protocol for a call to procedure.
func addProtocolTags(tags map[string]any, protocol *Protocol, procedure string) {
	if protocol.system != "" {
		tags[ext.RPCSystem] = protocol.system
	}
	if protocol.grpcStatus {
		tags[ext.GRPCFullMethod] = procedure
	}
}

// SetProtocolTags sets the tags of protocol for a call to procedure on a started span.
func SetProtocolTags(span *tracer.Span, protocol *Protocol, procedure string) {
	if protocol.system != "" {
		span.SetTag(ext.RPCSystem, protocol.system)
	}
	if protocol.grpcStatus {
		span.SetTag(ext.GRPCFullMethod, procedure)
	}
}

func parseProcedure(procedure string) (service, method string) {
	service, method, _ = strings.Cut(strings.TrimPrefix(procedure, "/"), "/")
	return service, method
}

func methodKind(streamType StreamType) string {
	switch streamType {
	case StreamTypeUnary:
		return methodKindUnary
	case StreamTypeClient:
		return methodKindClientStream
	case StreamTypeServer:
		return methodKindServerStream
	case StreamTypeBidi:
		return methodKindBidiStream
	default:
		return methodKindUnknown
	}
}

// PeerTags are the network.destination.* tags of a client's peer. Their values are boxed once, so
// that setting them on every message span of a stream doesn't allocate.
type PeerTags struct {
	ip, name, port any
}

// NewPeerTags returns the tags of the peer address addr, a host with or without a port.
func NewPeerTags(addr string) PeerTags {
	if addr == "" {
		return PeerTags{}
	}
	// SplitHostPort and Atoi allocate the errors they return: skip them when addr has no port, as a
	// URL host often doesn't.
	host, port := addr, ""
	if strings.LastIndexByte(addr, ':') > strings.LastIndexByte(addr, ']') {
		if h, p, err := net.SplitHostPort(addr); err == nil {
			host, port = h, p
		}
	}
	var tags PeerTags
	host = strings.Trim(host, "[]")
	// Unlike net.ParseIP, netip.ParseAddr accepts the zone of a link-local address, such as
	// fe80::1%lo0. Unmap tags IPv4-mapped addresses as IPv4, as net.IP.String does.
	if ip, err := netip.ParseAddr(host); err == nil {
		tags.ip = ipText(ip.Unmap(), host)
	} else if host != "" {
		tags.name = host
	}
	if port != "" {
		if portNumber, err := strconv.Atoi(port); err == nil {
			tags.port = portNumber
		}
	}
	return tags
}

// ipText returns the text form of ip, which was parsed from host: host itself if it already is in
// that form, which saves formatting a new string.
func ipText(ip netip.Addr, host string) string {
	var buf [64]byte
	if text := ip.AppendTo(buf[:0]); string(text) != host {
		return string(text)
	}
	return host
}

// Set sets the tags after span has started: as start tags, they would overflow the tracer's
// 8-entry small tag map on client message spans.
func (p PeerTags) Set(span *tracer.Span) {
	if p.ip != nil {
		span.SetTag(ext.NetworkDestinationIP, p.ip)
	}
	if p.name != nil {
		span.SetTag(ext.NetworkDestinationName, p.name)
	}
	if p.port != nil {
		span.SetTag(ext.NetworkDestinationPort, p.port)
	}
}

// SetMetadataTags tags span with the request metadata that all yields, if EnableMetadataTags was
// called.
func (c *Config) SetMetadataTags(span *tracer.Span, all iter.Seq2[string, []string]) {
	if c.withMetadataTags {
		setMetadataTags(span, all, c.ignoredMetadata)
	}
}

// setMetadataTags tags span with the request metadata that all yields, except for binary ("-bin")
// keys, baggage keys and the lowercase keys in ignored. SetTag copies a slice's elements into the
// span's tags before it returns, so the values are not retained.
func setMetadataTags(span *tracer.Span, all iter.Seq2[string, []string], ignored map[string]struct{}) {
	if span == nil {
		return
	}
	for key, values := range all {
		if key, ok := metadataKey(key, ignored); ok {
			span.SetTag(tagRequestMetadataPrefix+key, values)
		}
	}
}

// metadataKey returns the lowercased metadata key and whether setMetadataTags reports it.
func metadataKey(key string, ignored map[string]struct{}) (string, bool) {
	key = strings.ToLower(key)
	_, isIgnored := ignored[key]
	return key, !isIgnored && !strings.HasSuffix(key, "-bin") && !strings.HasPrefix(key, tracer.DefaultBaggageHeaderPrefix)
}

// SetRequestTags tags span with the JSON encoding of request, if it is a protobuf message and
// EnableRequestTags was called.
func (c *Config) SetRequestTags(span *tracer.Span, request any, protocol *Protocol) {
	if c.withRequestTags {
		setRequestTags(span, request, protocol)
	}
}

// setRequestTags tags span with the JSON encoding of request, if it is a protobuf message.
func setRequestTags(span *tracer.Span, request any, protocol *Protocol) {
	if span == nil {
		return
	}
	message, ok := request.(proto.Message)
	if !ok {
		return
	}
	encoded, err := protojson.Marshal(message)
	if err != nil {
		return
	}
	span.SetTag(protocol.requestTag, string(encoded))
}
