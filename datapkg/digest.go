package datapkg

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// digestOf is the SHA-256 over
//
//	"greenware.datapkg/1" 0x00 uint64be(len(S)) S uint64be(len(D)) D
//
// where S and D are the canonical forms of the schema and the data file.
func digestOf(schemaCanon, dataCanon []byte) string {
	h := sha256.New()
	h.Write([]byte(DigestPrefix))
	h.Write([]byte{0})
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(schemaCanon)))
	h.Write(n[:])
	h.Write(schemaCanon)
	binary.BigEndian.PutUint64(n[:], uint64(len(dataCanon)))
	h.Write(n[:])
	h.Write(dataCanon)
	return hex.EncodeToString(h.Sum(nil))
}

// Digest returns the pin of a schema file and a data file given as written:
// both are parsed strictly and put in canonical form, and the digest covers
// the canonical forms, so key order and white space do not change it. The
// result is 64 lower-case hex digits. It checks that both files are JSON and
// nothing else; Load checks them against each other.
func Digest(schemaBytes, dataBytes []byte) (string, error) {
	s, err := Canonical("schema", schemaBytes)
	if err != nil {
		return "", err
	}
	d, err := Canonical("data", dataBytes)
	if err != nil {
		return "", err
	}
	return digestOf(s, d), nil
}
