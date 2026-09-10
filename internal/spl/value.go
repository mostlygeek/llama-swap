package spl

import (
	"strings"

	"github.com/tidwall/gjson"
)

// value is a runtime value read from a request namespace.
type value struct {
	kind valueKind
	num  float64
	str  string
	b    bool
}

var absent = value{kind: kindAbsent}

func stringValue(s string) value {
	return value{kind: kindString, str: s}
}

// fromResult converts a gjson lookup into a value. A missing path is absent;
// an explicit JSON null is present with kind null.
func fromResult(r gjson.Result) value {
	if !r.Exists() {
		return absent
	}
	switch r.Type {
	case gjson.Null:
		return value{kind: kindNull}
	case gjson.True:
		return value{kind: kindBool, b: true}
	case gjson.False:
		return value{kind: kindBool, b: false}
	case gjson.Number:
		return value{kind: kindNumber, num: r.Num}
	case gjson.String:
		return value{kind: kindString, str: r.Str}
	default:
		if r.IsArray() {
			return value{kind: kindArray}
		}
		return value{kind: kindObject}
	}
}

// equals implements =. Values are equal only when they share a kind and
// compare equal; objects, arrays and absent values never compare equal.
func (v value) equals(lit literal) bool {
	if v.kind != lit.kind {
		return false
	}
	switch v.kind {
	case kindNull:
		return true
	case kindBool:
		return v.b == lit.b
	case kindNumber:
		return v.num == lit.num
	case kindString:
		return v.str == lit.str
	}
	return false
}

// compare implements the ordering operators. Only number-to-number and
// string-to-string comparisons are ordered; anything else is false.
func (v value) compare(op tokenKind, lit literal) bool {
	var c int
	switch {
	case v.kind == kindNumber && lit.kind == kindNumber:
		switch {
		case v.num < lit.num:
			c = -1
		case v.num > lit.num:
			c = 1
		}
	case v.kind == kindString && lit.kind == kindString:
		c = strings.Compare(v.str, lit.str)
	default:
		return false
	}
	switch op {
	case tokLt:
		return c < 0
	case tokLte:
		return c <= 0
	case tokGt:
		return c > 0
	case tokGte:
		return c >= 0
	}
	return false
}

// contextString coerces a literal for a context.<key> write. Strings are
// stored as-is, everything else as its JSON text. Returns ok=false for null,
// which callers treat as a delete.
func (lit literal) contextString() (string, bool) {
	switch lit.kind {
	case kindNull:
		return "", false
	case kindString:
		return lit.str, true
	default:
		return lit.raw, true
	}
}
