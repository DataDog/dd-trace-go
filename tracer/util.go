package tracer

func toFloat64(value interface{}) (f float64, ok bool) {
	switch i := value.(type) {
	case byte:
		return float64(i), true
	case float32:
		return float64(i), true
	case float64:
		return float64(i), true
	case int:
		return float64(i), true
	case int16:
		return float64(i), true
	case int32:
		return float64(i), true
	case int64:
		return float64(i), true
	case uint:
		return float64(i), true
	case uint16:
		return float64(i), true
	case uint32:
		return float64(i), true
	case uint64:
		return float64(i), true
	default:
		return 0, false
	}
}
