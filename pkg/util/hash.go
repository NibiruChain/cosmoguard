package util

import (
	"crypto/sha256"
	"encoding/hex"
)

func Sha256(content string) string {
	h := sha256.New()
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}
