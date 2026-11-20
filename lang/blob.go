package lang

import (
	"crypto/sha256"
	"encoding/hex"
)

// GrammarSHA256 returns the SHA-256 digest of the embedded grammar blob, in
// lower-case hexadecimal. Tools use it to say which grammar they run.
func GrammarSHA256() string {
	sum := sha256.Sum256(grammarBlob)
	return hex.EncodeToString(sum[:])
}
