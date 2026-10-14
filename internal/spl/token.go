package spl

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNewline
	tokIdent
	tokNumber // text holds the source spelling
	tokString // text holds the decoded value
	tokRegex  // text holds the pattern
	tokObject // text holds compact JSON
	tokDot
	tokLBracket
	tokRBracket
	tokLParen
	tokRParen
	tokComma
	tokEq
	tokNeq
	tokLt
	tokLte
	tokGt
	tokGte
)

type token struct {
	kind tokenKind
	text string
	line int
	col  int
}

// describe renders a token for error messages.
func (t token) describe() string {
	switch t.kind {
	case tokEOF:
		return "end of program"
	case tokNewline:
		return "end of line"
	case tokString:
		return `"` + t.text + `"`
	case tokRegex:
		return "/" + t.text + "/"
	case tokObject:
		return "object literal"
	default:
		return `"` + t.text + `"`
	}
}

// isKeyword reports whether the token is the identifier kw. Keywords are only
// meaningful in keyword positions, so a body key named "set" still works as a
// path segment.
func (t token) isKeyword(kw string) bool {
	return t.kind == tokIdent && t.text == kw
}

const (
	kwDefault = "default"
	kwSet     = "set"
	kwRemove  = "remove"
	kwApply   = "apply"
	kwDeny    = "deny"
	kwTo      = "to"
	kwWhen    = "when"
	kwAnd     = "and"
	kwOr      = "or"
	kwNot     = "not"
	kwIs      = "is"
	kwPresent = "present"
	kwMissing = "missing"
	kwIn      = "in"
	kwMatches = "matches"
	kwTrue    = "true"
	kwFalse   = "false"
	kwNull    = "null"
)
