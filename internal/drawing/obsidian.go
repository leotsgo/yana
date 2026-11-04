package drawing

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf16"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
)

// IsObsidian reports whether content is a drawing written by Obsidian's
// Excalidraw plugin, which marks its files with an excalidraw-plugin key.
func IsObsidian(content []byte) bool {
	for _, kv := range frontmatter.Parse(content).Meta.Raw {
		if kv[0] == "excalidraw-plugin" {
			return true
		}
	}
	return false
}

// FromObsidian converts a whole Obsidian Excalidraw file to a yana drawing:
// the frontmatter is kept minus the excalidraw-plugin key, and the body is
// replaced by the scene taken from the compressed-json block, or else the
// json block.
func FromObsidian(content []byte) ([]byte, error) {
	body := frontmatter.Parse(content).Body
	var scene []byte
	if raw, err := fence(body, "compressed-json"); err == nil {
		s, err := decompressFromBase64(strings.Join(strings.Fields(string(raw)), ""))
		if err != nil {
			return nil, err
		}
		scene = []byte(s)
	} else if raw, err := fence(body, "json"); err == nil {
		scene = raw
	} else {
		return nil, errors.New("drawing: no compressed-json or json block")
	}
	if err := Validate(scene); err != nil {
		return nil, err
	}
	out, _ := frontmatter.RemoveKey(content, "excalidraw-plugin")
	head := frontmatter.Parse(out).Head
	return append(bytes.Clone(head), Encode(scene)...), nil
}

// maxDecodedUnits caps the decoded UTF-16 units: a crafted stream can make
// lz-string output grow quadratically. A var so a test can lower it.
var maxDecodedUnits = 32 << 20

const lzAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="

// decompressFromBase64 is lz-string 1.5.0's decompressFromBase64. lz-string
// works on UTF-16 code units, so the output is built as such and decoded at
// the end.
func decompressFromBase64(s string) (string, error) {
	bad := errors.New("drawing: bad compressed-json data")
	vals := make([]int, len(s))
	for i := range len(s) {
		v := strings.IndexByte(lzAlphabet, s[i])
		if v < 0 {
			return "", bad
		}
		vals[i] = v
	}
	// reading state: val is the current 6-bit group, pos its next bit mask
	index, val, pos := 1, 0, 32
	if len(vals) > 0 {
		val = vals[0]
	}
	readBits := func(n int) int {
		bits := 0
		for p := 0; p < n; p++ {
			if val&pos != 0 {
				bits |= 1 << p
			}
			pos >>= 1
			if pos == 0 {
				pos = 32
				val = 0
				if index < len(vals) {
					val = vals[index]
				}
				index++
			}
		}
		return bits
	}
	dict := [][]uint16{{0}, {1}, {2}}
	enlargeIn, numBits := 4, 3
	var c []uint16
	switch readBits(2) {
	case 0:
		c = []uint16{uint16(readBits(8))}
	case 1:
		c = []uint16{uint16(readBits(16))}
	default:
		return "", nil
	}
	dict = append(dict, c)
	w := c
	result := append([]uint16(nil), c...)
	grow := func() {
		if enlargeIn == 0 {
			enlargeIn = 1 << numBits
			numBits++
		}
	}
	for {
		if index > len(vals) {
			return "", bad
		}
		code := readBits(numBits)
		switch code {
		case 0:
			dict = append(dict, []uint16{uint16(readBits(8))})
			code = len(dict) - 1
			enlargeIn--
		case 1:
			dict = append(dict, []uint16{uint16(readBits(16))})
			code = len(dict) - 1
			enlargeIn--
		case 2:
			return string(utf16.Decode(result)), nil
		}
		grow()
		var entry []uint16
		switch {
		case code < len(dict):
			entry = dict[code]
		case code == len(dict):
			entry = append(append([]uint16(nil), w...), w[0])
		default:
			return "", bad
		}
		if len(result)+len(entry) > maxDecodedUnits {
			return "", errors.New("drawing: compressed-json data is too large")
		}
		result = append(result, entry...)
		dict = append(dict, append(append([]uint16(nil), w...), entry[0]))
		enlargeIn--
		w = entry
		grow()
	}
}
