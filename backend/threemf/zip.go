package threemf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
)

// The ZIP writer and reader here are the minimum 3MF needs: stored entries,
// no ZIP64, no encryption, no data descriptors. The writer's output depends
// on the entry names and bytes alone.

const (
	sigLocal   = 0x04034b50
	sigCentral = 0x02014b50
	sigEnd     = 0x06054b50

	// dosDate is 1980-01-01, the earliest date a ZIP can hold; dosTime is
	// 00:00:00.
	dosDate = 0<<9 | 1<<5 | 1
	dosTime = 0

	zipVersion = 20
	maxZip     = 1<<32 - 1
)

type zipEntry struct {
	Name string
	Data []byte
}

// writeZip returns a ZIP with the entries sorted by name (bytewise), stored,
// stamped 1980-01-01, with no extra field and an explicit CRC-32.
func writeZip(entries []zipEntry) ([]byte, error) {
	es := append([]zipEntry(nil), entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	if len(es) > 0xFFFF {
		return nil, errors.New("threemf: too many package parts")
	}
	var out []byte
	type rec struct {
		e      zipEntry
		crc    uint32
		offset uint32
	}
	var recs []rec
	le := binary.LittleEndian
	for i, e := range es {
		if i > 0 && es[i-1].Name == e.Name {
			return nil, fmt.Errorf("threemf: duplicate package part %q", e.Name)
		}
		if len(out) > maxZip || len(e.Data) > maxZip || len(e.Name) > 0xFFFF {
			return nil, errors.New("threemf: the package is larger than a ZIP without ZIP64 can hold")
		}
		r := rec{e: e, crc: crc32.ChecksumIEEE(e.Data), offset: uint32(len(out))}
		recs = append(recs, r)
		h := make([]byte, 30)
		le.PutUint32(h[0:], sigLocal)
		le.PutUint16(h[4:], zipVersion)
		le.PutUint16(h[6:], 0) // flags
		le.PutUint16(h[8:], 0) // method: stored
		le.PutUint16(h[10:], dosTime)
		le.PutUint16(h[12:], dosDate)
		le.PutUint32(h[14:], r.crc)
		le.PutUint32(h[18:], uint32(len(e.Data)))
		le.PutUint32(h[22:], uint32(len(e.Data)))
		le.PutUint16(h[26:], uint16(len(e.Name)))
		le.PutUint16(h[28:], 0) // extra length
		out = append(out, h...)
		out = append(out, e.Name...)
		out = append(out, e.Data...)
	}
	cdStart := len(out)
	for _, r := range recs {
		h := make([]byte, 46)
		le.PutUint32(h[0:], sigCentral)
		le.PutUint16(h[4:], zipVersion) // version made by: MS-DOS, 2.0
		le.PutUint16(h[6:], zipVersion)
		le.PutUint16(h[8:], 0)
		le.PutUint16(h[10:], 0)
		le.PutUint16(h[12:], dosTime)
		le.PutUint16(h[14:], dosDate)
		le.PutUint32(h[16:], r.crc)
		le.PutUint32(h[20:], uint32(len(r.e.Data)))
		le.PutUint32(h[24:], uint32(len(r.e.Data)))
		le.PutUint16(h[28:], uint16(len(r.e.Name)))
		// extra, comment, disk, internal and external attributes: zero
		le.PutUint32(h[42:], r.offset)
		out = append(out, h...)
		out = append(out, r.e.Name...)
	}
	cdSize := len(out) - cdStart
	if len(out) > maxZip {
		return nil, errors.New("threemf: the package is larger than a ZIP without ZIP64 can hold")
	}
	h := make([]byte, 22)
	le.PutUint32(h[0:], sigEnd)
	le.PutUint16(h[8:], uint16(len(recs)))
	le.PutUint16(h[10:], uint16(len(recs)))
	le.PutUint32(h[12:], uint32(cdSize))
	le.PutUint32(h[16:], uint32(cdStart))
	out = append(out, h...)
	return out, nil
}

// maxPart bounds one part a reader accepts.
const maxPart = 1 << 30

// readZip reads a ZIP of stored entries, checking every CRC-32. It rejects
// compressed entries, ZIP64, an archive comment and any entry whose local
// header disagrees with the central directory.
func readZip(b []byte) (map[string][]byte, error) {
	le := binary.LittleEndian
	if len(b) < 22 || le.Uint32(b[len(b)-22:]) != sigEnd {
		return nil, errors.New("threemf: not a ZIP file (no end record, or an archive comment)")
	}
	end := b[len(b)-22:]
	n := int(le.Uint16(end[10:]))
	cdSize := int(le.Uint32(end[12:]))
	cdOff := int(le.Uint32(end[16:]))
	if cdOff < 0 || cdSize < 0 || cdOff+cdSize != len(b)-22 {
		return nil, errors.New("threemf: the central directory does not end at the end record")
	}
	parts := map[string][]byte{}
	p := cdOff
	for i := 0; i < n; i++ {
		if p+46 > len(b) || le.Uint32(b[p:]) != sigCentral {
			return nil, errors.New("threemf: bad central directory entry")
		}
		h := b[p : p+46]
		method := le.Uint16(h[10:])
		crc := le.Uint32(h[16:])
		csize, usize := le.Uint32(h[20:]), le.Uint32(h[24:])
		nl, xl, cl := int(le.Uint16(h[28:])), int(le.Uint16(h[30:])), int(le.Uint16(h[32:]))
		off := int(le.Uint32(h[42:]))
		if p+46+nl+xl+cl > len(b) {
			return nil, errors.New("threemf: central directory entry runs past the file")
		}
		name := string(b[p+46 : p+46+nl])
		p += 46 + nl + xl + cl
		if method != 0 || csize != usize {
			return nil, fmt.Errorf("threemf: part %q is compressed; only stored parts are read", name)
		}
		if usize > maxPart {
			return nil, fmt.Errorf("threemf: part %q is too large", name)
		}
		if off < 0 || off+30 > len(b) || le.Uint32(b[off:]) != sigLocal {
			return nil, fmt.Errorf("threemf: part %q has no local header", name)
		}
		lh := b[off : off+30]
		lnl, lxl := int(le.Uint16(lh[26:])), int(le.Uint16(lh[28:]))
		start := off + 30 + lnl + lxl
		if start+int(usize) > cdOff || string(b[off+30:off+30+lnl]) != name {
			return nil, fmt.Errorf("threemf: part %q: local header and central directory disagree", name)
		}
		data := b[start : start+int(usize)]
		if crc32.ChecksumIEEE(data) != crc {
			return nil, fmt.Errorf("threemf: part %q: CRC-32 mismatch", name)
		}
		if _, dup := parts[name]; dup {
			return nil, fmt.Errorf("threemf: part %q appears twice", name)
		}
		parts[name] = data
	}
	if p != cdOff+cdSize {
		return nil, errors.New("threemf: extra bytes in the central directory")
	}
	return parts, nil
}
