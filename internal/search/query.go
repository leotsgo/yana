// Search operators: the small grammar the search box accepts. One parser
// lives here and is the only authority on what a term means; the server
// maps the parsed terms onto the index, and the web client parses the
// same grammar (web/src/opsearch.ts) to highlight operators as chips,
// offer completions, and filter the switcher. Anything the grammar does
// not recognise is plain full text — an unknown operator is searched,
// never rejected.
package search

import (
	"strings"
	"time"
)

// Operator names.
const (
	OpTag      = "tag"
	OpPath     = "path"
	OpSpace    = "space"
	OpIs       = "is"
	OpHas      = "has"
	OpAuthor   = "author"
	OpBefore   = "before"
	OpAfter    = "after"
	dateFormat = "2006-01-02"
)

// isValues are the is: selectors.
var isValues = map[string]bool{
	"untagged": true,
	"task":     true,
	"html":     true,
}

// hasValues are the has: selectors.
var hasValues = map[string]bool{
	"image":      true,
	"attachment": true,
}

// Term is one token of a query. Op is empty for plain text; Value holds
// the operator's argument, Text the plain text of a word or phrase.
type Term struct {
	// Raw is the term exactly as typed, negation mark included.
	Raw string
	// Negated says the term excludes what it matches (-tag:x).
	Negated bool
	// Op is the operator name (tag, path, …) or "" for plain text.
	Op string
	// Value is the operator's argument.
	Value string
	// Date is Value parsed, for before:/after: only.
	Date time.Time
	// Quoted says a text term was written in double quotes (an exact
	// phrase) — or that an operator's value was quoted.
	Quoted bool
	// Text is the searchable text of a plain term ("" for operators).
	Text string
}

// Query is a parsed search query.
type Query struct {
	Terms []Term
}

// TextTerms returns the plain text terms, positive and negated apart.
// Words too short for the trigram index ride along; the caller decides
// how to run them.
func (q Query) TextTerms() (positive, negative []Term) {
	for _, t := range q.Terms {
		if t.Op == "" && t.Text != "" {
			if t.Negated {
				negative = append(negative, t)
			} else {
				positive = append(positive, t)
			}
		}
	}
	return positive, negative
}

// HasFilters reports whether any operator term is present.
func (q Query) HasFilters() bool {
	for _, t := range q.Terms {
		if t.Op != "" {
			return true
		}
	}
	return false
}

// Parse splits a query into terms. Whitespace separates terms; a term is
// an operator (name:value, the value optionally double-quoted), a bare
// #tag, a double-quoted exact phrase, or a plain word. A leading -
// negates the term that follows it. A name that is not a known operator,
// an is:/has: selector that does not exist, or a date that is not
// YYYY-MM-DD demotes the whole term to plain text.
func Parse(q string) Query {
	var terms []Term
	rs := []rune(strings.TrimSpace(q))
	i := 0
	for i < len(rs) {
		for i < len(rs) && isSpace(rs[i]) {
			i++
		}
		if i >= len(rs) {
			break
		}
		start := i
		negated := false
		if rs[i] == '-' && i+1 < len(rs) && !isSpace(rs[i+1]) {
			negated = true
			i++
		}
		if i >= len(rs) {
			break
		}
		switch {
		case rs[i] == '"':
			content, end := quotedRun(rs, i)
			terms = append(terms, Term{
				Raw: string(rs[start:end]), Negated: negated,
				Quoted: true, Text: content,
			})
			i = end
		case rs[i] == '#' && i+1 < len(rs) && !isSpace(rs[i+1]):
			j := wordEnd(rs, i+1)
			terms = append(terms, Term{
				Raw: string(rs[start:j]), Negated: negated,
				Op: OpTag, Value: strings.ToLower(string(rs[i+1 : j])),
			})
			i = j
		default:
			name, opLen := opName(rs, i)
			if opLen > 0 {
				valStart := i + opLen
				if valStart < len(rs) && rs[valStart] == '"' {
					v, end := quotedRun(rs, valStart)
					terms = append(terms, operatorTerm(rs, start, valStart, end, negated, name, v, true))
					i = end
				} else {
					j := wordEnd(rs, valStart)
					v := string(rs[valStart:j])
					terms = append(terms, operatorTerm(rs, start, valStart, j, negated, name, v, false))
					i = j
				}
			} else {
				j := wordEnd(rs, i)
				terms = append(terms, Term{
					Raw: string(rs[start:j]), Negated: negated,
					Text: string(rs[i:j]),
				})
				i = j
			}
		}
	}
	return Query{Terms: terms}
}

// operatorTerm builds an operator term, or a plain-text term when the
// operator or its value does not hold up. valStart is the index of the
// value's first rune (or its opening quote); rawEnd is the term's last
// index, both computed by Parse.
func operatorTerm(rs []rune, start, valStart, rawEnd int, negated bool, name, value string, quoted bool) Term {
	demote := func() Term {
		return Term{Raw: string(rs[start:rawEnd]), Negated: negated, Text: string(rs[start+negLen(negated) : rawEnd])}
	}
	switch name {
	case OpIs:
		if !isValues[strings.ToLower(value)] {
			return demote()
		}
		value = strings.ToLower(value)
	case OpHas:
		if !hasValues[strings.ToLower(value)] {
			return demote()
		}
		value = strings.ToLower(value)
	case OpBefore, OpAfter:
		d, err := time.Parse(dateFormat, value)
		if err != nil {
			return demote()
		}
		return Term{Raw: string(rs[start:rawEnd]), Negated: negated, Op: name, Value: value, Date: d.UTC(), Quoted: quoted}
	case OpTag:
		if value == "" {
			return demote()
		}
		value = strings.ToLower(value)
	case OpPath, OpSpace, OpAuthor:
		if value == "" {
			return demote()
		}
	default:
		return demote()
	}
	return Term{Raw: string(rs[start:rawEnd]), Negated: negated, Op: name, Value: value, Quoted: quoted}
}

func negLen(negated bool) int {
	if negated {
		return 1
	}
	return 0
}

// quotedRun reads the quoted run starting at the quote rs[open]: it
// returns the content between the quotes and the index just past the
// closing quote, or the end of the string when it never closes.
func quotedRun(rs []rune, open int) (string, int) {
	for i := open + 1; i < len(rs); i++ {
		if rs[i] == '"' {
			return string(rs[open+1 : i]), i + 1
		}
	}
	return string(rs[open+1:]), len(rs)
}

// wordEnd returns the index of the first space at or after i.
func wordEnd(rs []rune, i int) int {
	for i < len(rs) && !isSpace(rs[i]) {
		i++
	}
	return i
}

// opName reads a known lowercase operator name followed by a colon at
// i, returning the name and its length including the colon; 0 when the
// run of letters is not a known operator.
func opName(rs []rune, i int) (string, int) {
	j := i
	for j < len(rs) && rs[j] >= 'a' && rs[j] <= 'z' {
		j++
	}
	if j == i || j >= len(rs) || rs[j] != ':' {
		return "", 0
	}
	name := string(rs[i:j])
	switch name {
	case OpTag, OpPath, OpSpace, OpIs, OpHas, OpAuthor, OpBefore, OpAfter:
		return name, j - i + 1
	}
	return "", 0
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
