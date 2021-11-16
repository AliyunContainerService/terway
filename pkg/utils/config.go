package utils

func Minimal(a int) int {
	if a < 0 {
		return 0
	}
	return a
}

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
