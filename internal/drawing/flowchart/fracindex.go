package flowchart

import (
	"errors"
	"fmt"
	"strings"
)

// Port of rocicorp's "fractional-indexing" (CC0), base62 digits.

const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var smallestInt = "A" + strings.Repeat("0", 26)

func intLen(head byte) (int, error) {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2, nil
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2, nil
	}
	return 0, fmt.Errorf("invalid order key head: %q", head)
}

func intPart(key string) (string, error) {
	if key == "" {
		return "", errors.New("empty order key")
	}
	n, err := intLen(key[0])
	if err != nil {
		return "", err
	}
	if n > len(key) {
		return "", fmt.Errorf("invalid order key: %s", key)
	}
	return key[:n], nil
}

func validKey(key string) error {
	if key == smallestInt {
		return fmt.Errorf("invalid order key: %s", key)
	}
	i, err := intPart(key)
	if err != nil {
		return err
	}
	if strings.HasSuffix(key[len(i):], "0") {
		return fmt.Errorf("invalid order key: %s", key)
	}
	return nil
}

func incInt(x string) (string, bool) {
	head, d := x[0], []byte(x[1:])
	carry := true
	for i := len(d) - 1; carry && i >= 0; i-- {
		n := strings.IndexByte(digits, d[i]) + 1
		if n == len(digits) {
			d[i] = digits[0]
		} else {
			d[i] = digits[n]
			carry = false
		}
	}
	if carry {
		if head == 'Z' {
			return "a" + digits[:1], true
		}
		if head == 'z' {
			return "", false
		}
		head++
		if head > 'a' {
			d = append(d, digits[0])
		} else {
			d = d[:len(d)-1]
		}
	}
	return string(head) + string(d), true
}

func decInt(x string) (string, bool) {
	head, d := x[0], []byte(x[1:])
	last := digits[len(digits)-1]
	borrow := true
	for i := len(d) - 1; borrow && i >= 0; i-- {
		n := strings.IndexByte(digits, d[i]) - 1
		if n == -1 {
			d[i] = last
		} else {
			d[i] = digits[n]
			borrow = false
		}
	}
	if borrow {
		if head == 'a' {
			return "Z" + string(last), true
		}
		if head == 'A' {
			return "", false
		}
		head--
		if head < 'Z' {
			d = append(d, last)
		} else {
			d = d[:len(d)-1]
		}
	}
	return string(head) + string(d), true
}

func midpoint(a, b string) (string, error) {
	if b != "" && a >= b {
		return "", fmt.Errorf("%s >= %s", a, b)
	}
	if strings.HasSuffix(a, "0") || strings.HasSuffix(b, "0") {
		return "", errors.New("trailing zero")
	}
	if b != "" {
		n := 0
		for {
			ca := byte('0')
			if n < len(a) {
				ca = a[n]
			}
			if n >= len(b) || ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			rest := ""
			if n < len(a) {
				rest = a[n:]
			}
			m, err := midpoint(rest, b[n:])
			return b[:n] + m, err
		}
	}
	da, db := 0, len(digits)
	if a != "" {
		da = strings.IndexByte(digits, a[0])
	}
	if b != "" {
		db = strings.IndexByte(digits, b[0])
	}
	if db-da > 1 {
		return string(digits[(da+db+1)/2]), nil // round(0.5*(da+db))
	}
	if len(b) > 1 {
		return b[:1], nil
	}
	rest := ""
	if a != "" {
		rest = a[1:]
	}
	m, err := midpoint(rest, "")
	return string(digits[da]) + m, err
}

// keyBetween returns a key sorting strictly between a and b ("" is an open end).
func keyBetween(a, b string) (string, error) {
	if a != "" {
		if err := validKey(a); err != nil {
			return "", err
		}
	}
	if b != "" {
		if err := validKey(b); err != nil {
			return "", err
		}
	}
	if a != "" && b != "" && a >= b {
		return "", fmt.Errorf("%s >= %s", a, b)
	}
	if a == "" {
		if b == "" {
			return "a0", nil
		}
		ib, _ := intPart(b)
		if ib == smallestInt {
			m, err := midpoint("", b[len(ib):])
			return ib + m, err
		}
		if ib < b {
			return ib, nil
		}
		r, ok := decInt(ib)
		if !ok {
			return "", errors.New("cannot decrement any more")
		}
		return r, nil
	}
	ia, _ := intPart(a)
	if b == "" {
		if i, ok := incInt(ia); ok {
			return i, nil
		}
		m, err := midpoint(a[len(ia):], "")
		return ia + m, err
	}
	ib, _ := intPart(b)
	if ia == ib {
		m, err := midpoint(a[len(ia):], b[len(ib):])
		return ia + m, err
	}
	i, ok := incInt(ia)
	if !ok {
		return "", errors.New("cannot increment any more")
	}
	if i < b {
		return i, nil
	}
	m, err := midpoint(a[len(ia):], "")
	return ia + m, err
}

// nKeysBetween returns n increasing keys strictly between a and b.
func nKeysBetween(a, b string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	if n == 1 {
		k, err := keyBetween(a, b)
		return []string{k}, err
	}
	if b == "" {
		c, err := keyBetween(a, "")
		if err != nil {
			return nil, err
		}
		out := []string{c}
		for i := 1; i < n; i++ {
			if c, err = keyBetween(c, ""); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, nil
	}
	if a == "" {
		c, err := keyBetween("", b)
		if err != nil {
			return nil, err
		}
		out := []string{c}
		for i := 1; i < n; i++ {
			if c, err = keyBetween("", c); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out, nil
	}
	c, err := keyBetween(a, b)
	if err != nil {
		return nil, err
	}
	lo, err := nKeysBetween(a, c, n/2)
	if err != nil {
		return nil, err
	}
	hi, err := nKeysBetween(c, b, n-n/2-1)
	if err != nil {
		return nil, err
	}
	return append(append(lo, c), hi...), nil
}
