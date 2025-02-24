package utils

func ContainsAll[K comparable, V comparable](superset, subset map[K]V) bool {
	for key, value := range subset {
		if supersetValue, exists := superset[key]; !exists || supersetValue != value {
			return false
		}
	}
	return true
}
