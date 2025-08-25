package streamprocessingoffload

import (
	"fmt"
	"net/http"

	"github.com/negasus/haproxy-spoe-go/varint"
)

// readUvarintAt reads a Peers varint starting at index p and returns the decoded value
// and the next index to read from. It validates bounds and varint correctness.
func readUvarintAt(buf []byte, p int) (val uint64, next int, err error) {
	if p >= len(buf) {
		return 0, p, fmt.Errorf("unexpected end of headers at index %d", p)
	}
	v, n := varint.Uvarint(buf[p:])
	if n < 0 {
		return 0, p, fmt.Errorf("invalid varint at index %d", p)
	}
	return v, p + n, nil
}

// parseHAProxyReqHdrsBin decodes HAProxy's req.hdrs_bin format into an http.Header.
// Format: repeated pairs of <str:name><str:value> where str is <varint:length><bytes>.
// The list is terminated by a pair of empty strings (length 0 for both name and value).
// https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/#7.3.6-req.hdrs_bin
func parseHAProxyReqHdrsBin(buf []byte) (http.Header, error) {
	if buf == nil || len(buf) == 0 {
		return nil, fmt.Errorf("empty headers buffer")
	}

	headers := make(http.Header)

	p := 0
	for {
		// Read name length
		nameLen, next, err := readUvarintAt(buf, p)
		if err != nil {
			return nil, fmt.Errorf("read name length: %w", err)
		}
		p = next

		if nameLen > uint64(len(buf)-p) {
			return nil, fmt.Errorf("header name length %d exceeds remaining buffer %d", nameLen, len(buf)-p)
		}

		nameStart := p
		nameEnd := p + int(nameLen)
		name := string(buf[nameStart:nameEnd])
		p = nameEnd

		// Read value length
		valueLen, next, err := readUvarintAt(buf, p)
		if err != nil {
			return nil, fmt.Errorf("read value length for '%s': %w", name, err)
		}
		p = next

		// Termination marker: both lengths zero
		if nameLen == 0 && valueLen == 0 {
			break
		}

		if nameLen == 0 {
			return nil, fmt.Errorf("encountered empty header name with non-empty value")
		}

		if valueLen > uint64(len(buf)-p) {
			return nil, fmt.Errorf("header value length %d exceeds remaining buffer %d", valueLen, len(buf)-p)
		}

		valueStart := p
		valueEnd := p + int(valueLen)
		value := string(buf[valueStart:valueEnd])
		p = valueEnd

		headers.Add(name, value)
	}

	return headers, nil
}
