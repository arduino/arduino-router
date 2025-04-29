package msgpackrpc

import "math"

// ToInt converts a value of any type to an int. It returns the converted int and a boolean indicating success.
func ToInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int(v), true
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		return int(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

// ToUint converts a value of any type to an uint. It returns the converted int and a boolean indicating success.
func ToUint(value any) (uint, bool) {
	switch v := value.(type) {
	case int:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int8:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int16:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int32:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case uint:
		return v, true
	case uint8:
		return uint(v), true
	case uint16:
		return uint(v), true
	case uint32:
		return uint(v), true
	case uint64:
		return uint(v), true
	default:
		return 0, false
	}
}
