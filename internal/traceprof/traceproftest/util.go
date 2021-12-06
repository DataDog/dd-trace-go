package traceproftest

import "strconv"

// ValidSpanID returns true if id is a valid span id (random.Uint64()).
func ValidSpanID(id string) bool {
	val, err := strconv.ParseUint(id, 10, 64)
	return err == nil && val > 0
}
