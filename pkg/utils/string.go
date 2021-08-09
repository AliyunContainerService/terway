package utils

// TrimStr witch len
func TrimStr(s string, l int) string {
	if len(s) < l {
		return s
	}
	return s[:l]
}
