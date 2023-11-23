package util

import "strings"

func SliceContainsStringIgnoreCase(l []string, key string) bool {
	for _, k := range l {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}
