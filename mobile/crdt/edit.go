package crdt

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	ycrdt "github.com/reearth/ygo/crdt"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// toIndex converts a bound index or length to an int and keeps it in
// range on 32-bit targets.
func toIndex(v int64) (int, error) {
	if v < 0 {
		return 0, fmt.Errorf("yana/crdt: index must be >= 0, got %d", v)
	}
	if v > math.MaxInt32 {
		return 0, fmt.Errorf("yana/crdt: index %d too large", v)
	}
	return int(v), nil
}

// checkUTF8 rejects invalid UTF-8 before it reaches the port, which
// panics on it. A Kotlin string is always valid; this guards other
// callers.
func checkUTF8(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("yana/crdt: text is not valid UTF-8")
	}
	return nil
}

// Insert inserts s at pos, measured in UTF-16 code units. It returns the
// update to forward to the server, or nil when s is empty. pos must not
// fall inside a surrogate pair; a text-field cursor never does.
func (y *Doc) Insert(pos int64, s string) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil, ErrClosed
	}
	if s == "" {
		return nil, nil
	}
	if err := checkUTF8(s); err != nil {
		return nil, err
	}
	p, err := toIndex(pos)
	if err != nil {
		return nil, err
	}
	y.out = nil
	err = y.d.TransactE(func(txn *ycrdt.Transaction) error {
		t := txn.GetText(TextName)
		if p > t.Len() {
			return fmt.Errorf("yana/crdt: insert index %d out of range [0, %d]", p, t.Len())
		}
		t.Insert(txn, p, s, nil)
		return nil
	}, y.local)
	if err != nil {
		y.out = nil
		return nil, err
	}
	return y.take(), nil
}

// Delete removes n UTF-16 code units at pos. It returns the update to
// forward to the server, or nil when n is 0. The range [pos, pos+n) must
// lie inside the body and must not split a surrogate pair.
func (y *Doc) Delete(pos, n int64) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil, ErrClosed
	}
	if n == 0 {
		return nil, nil
	}
	p, l, err := toIndex2(pos, n)
	if err != nil {
		return nil, err
	}
	y.out = nil
	err = y.d.TransactE(func(txn *ycrdt.Transaction) error {
		t := txn.GetText(TextName)
		if p+l > t.Len() {
			return fmt.Errorf("yana/crdt: delete range [%d, %d) out of range [0, %d]", p, p+l, t.Len())
		}
		t.Delete(txn, p, l)
		return nil
	}, y.local)
	if err != nil {
		y.out = nil
		return nil, err
	}
	return y.take(), nil
}

// Edit replaces del UTF-16 code units at pos with s, in one transaction:
// the shape a text field's diff produces for one keystroke, paste, or
// deletion. It returns the update to forward, or nil when it changed
// nothing (del 0 and s empty). pos must not fall inside a surrogate pair;
// a text-field cursor never does. The single transaction means the edit
// undoes and redoes as one step.
func (y *Doc) Edit(pos, del int64, s string) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil, ErrClosed
	}
	if del < 0 {
		return nil, fmt.Errorf("yana/crdt: delete length must be >= 0, got %d", del)
	}
	if err := checkUTF8(s); err != nil {
		return nil, err
	}
	p, l, err := toIndex2(pos, del)
	if err != nil {
		return nil, err
	}
	if l == 0 && s == "" {
		return nil, nil
	}
	y.out = nil
	err = y.d.TransactE(func(txn *ycrdt.Transaction) error {
		t := txn.GetText(TextName)
		if p+l > t.Len() {
			return fmt.Errorf("yana/crdt: edit range [%d, %d) out of range [0, %d]", p, p+l, t.Len())
		}
		if l > 0 {
			t.Delete(txn, p, l)
		}
		if s != "" {
			t.Insert(txn, p, s, nil)
		}
		return nil
	}, y.local)
	if err != nil {
		y.out = nil
		return nil, err
	}
	return y.take(), nil
}

// editHunk is one replacement of a multi-region edit as the JSON in
// EditMany spells it.
type editHunk struct {
	P int64  `json:"p"`
	D int64  `json:"d"`
	I string `json:"i"`
}

// EditMany applies several replacements in one transaction, so a
// change the person sees as one thing — a formatting button wrapping a
// selection in marks, a prefix rewritten on every selected line — is
// one update, one undo step, the way a CodeMirror transaction is on
// the web. ops is a JSON array of hunks, each {"p":pos,"d":del,"i":ins}
// with UTF-16 positions measured against the text as it stands before
// the call; hunks must not overlap. Order does not matter: the hunks
// are applied from the last position to the first, where each earlier
// position is still valid. It returns the update to forward, or nil
// when the hunks changed nothing.
func (y *Doc) EditMany(ops string) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil, ErrClosed
	}
	var hunks []editHunk
	if err := json.Unmarshal([]byte(ops), &hunks); err != nil {
		return nil, fmt.Errorf("yana/crdt: ops are not a hunk array: %w", err)
	}
	if len(hunks) == 0 {
		return nil, nil
	}
	// Validate every hunk against the text as it stands, then apply
	// from the end so positions stay what the caller measured.
	sort.SliceStable(hunks, func(a, b int) bool { return hunks[a].P > hunks[b].P })
	length := utf16Len(y.text.ToString())
	for i, h := range hunks {
		if h.P < 0 || h.D < 0 {
			return nil, fmt.Errorf("yana/crdt: hunk %d has a negative position or length", i)
		}
		if err := checkUTF8(h.I); err != nil {
			return nil, err
		}
		p, l, err := toIndex2(h.P, h.D)
		if err != nil {
			return nil, err
		}
		if p+l > length {
			return nil, fmt.Errorf("yana/crdt: hunk %d range [%d, %d) out of range [0, %d)", i, p, p+l, length)
		}
		if i > 0 {
			prev := hunks[i-1]
			if int64(p+l) > prev.P {
				return nil, fmt.Errorf("yana/crdt: hunk %d overlaps the hunk before it", i)
			}
		}
	}
	sawChange := false
	for _, h := range hunks {
		if h.D == 0 && h.I == "" {
			continue
		}
		sawChange = true
	}
	if !sawChange {
		return nil, nil
	}
	y.out = nil
	err := y.d.TransactE(func(txn *ycrdt.Transaction) error {
		t := txn.GetText(TextName)
		for _, h := range hunks {
			if h.D == 0 && h.I == "" {
				continue
			}
			if h.D > 0 {
				p, l, _ := toIndex2(h.P, h.D)
				t.Delete(txn, p, l)
			}
			if h.I != "" {
				p, _ := toIndex(h.P)
				t.Insert(txn, p, h.I, nil)
			}
		}
		return nil
	}, y.local)
	if err != nil {
		y.out = nil
		return nil, err
	}
	return y.take(), nil
}

// ReplaceText mutates the body so it reads as want, expressed as the
// minimal insert and delete operations between the current text and
// want. Characters left alone keep their identity, which is what lets a
// wholesale replacement — a paste over a selection, a reload — merge
// with concurrent typing instead of reverting it. It returns the update
// to forward, or nil when the body already reads as want.
func (y *Doc) ReplaceText(want string) []byte {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil
	}
	want = strings.ToValidUTF8(want, "\uFFFD")
	have := y.text.ToString()
	if have == want {
		return nil
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffCleanupSemantic(dmp.DiffMain(have, want, false))
	y.out = nil
	y.d.Transact(func(txn *ycrdt.Transaction) {
		pos := 0
		for _, d := range diffs {
			n := utf16Len(d.Text)
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				pos += n
			case diffmatchpatch.DiffDelete:
				y.text.Delete(txn, pos, n)
			case diffmatchpatch.DiffInsert:
				y.text.Insert(txn, pos, d.Text, nil)
				pos += n
			}
		}
	}, y.local)
	return y.take()
}

// take returns the updates captured since out was cleared, as one. A
// single call emits one update; applying a bundle may emit several.
func (y *Doc) take() []byte {
	var merged []byte
	switch len(y.out) {
	case 0:
		return nil
	case 1:
		merged = y.out[0]
	default:
		// Merging valid updates never fails; if it somehow does, fall
		// back to the last incremental update rather than losing the
		// caller's broadcast entirely.
		if m, err := ycrdt.MergeUpdatesV1(y.out...); err == nil {
			merged = m
		} else {
			merged = y.out[len(y.out)-1]
		}
	}
	y.out = nil
	if isEmptyUpdate(merged) {
		return nil
	}
	return merged
}

// isEmptyUpdate reports whether a V1 update carries no content: the
// port emits these two bytes for a transaction that changed nothing.
func isEmptyUpdate(u []byte) bool {
	return len(u) == 2 && u[0] == 0 && u[1] == 0
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func toIndex2(a, b int64) (int, int, error) {
	x, err := toIndex(a)
	if err != nil {
		return 0, 0, err
	}
	z, err := toIndex(b)
	if err != nil {
		return 0, 0, err
	}
	return x, z, nil
}
