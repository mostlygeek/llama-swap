package spl

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type lexer struct {
	src    []rune
	pos    int
	line   int
	col    int
	depth  int // nesting inside ( ) and [ ]; newlines are insignificant when > 0
	tokens []token
}

// lex splits src into tokens. Newlines are significant clause separators
// except inside brackets, after a comment-only or blank line, or before a
// continuation keyword (when, and, or).
func lex(src string) ([]token, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	l := &lexer{src: []rune(src), line: 1, col: 1}
	if err := l.run(); err != nil {
		return nil, err
	}
	return l.finish(), nil
}

func (l *lexer) peekRune(offset int) rune {
	if l.pos+offset < len(l.src) {
		return l.src[l.pos+offset]
	}
	return 0
}

func (l *lexer) advance() rune {
	ch := l.src[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *lexer) emit(kind tokenKind, text string, line, col int) {
	l.tokens = append(l.tokens, token{kind: kind, text: text, line: line, col: col})
}

func (l *lexer) run() error {
	for l.pos < len(l.src) {
		ch := l.peekRune(0)
		line, col := l.line, l.col

		switch {
		case ch == '\n':
			l.advance()
			if l.depth == 0 {
				l.emit(tokNewline, "\n", line, col)
			}
		case ch == ' ' || ch == '\t':
			l.advance()
		case ch == '#':
			for l.pos < len(l.src) && l.peekRune(0) != '\n' {
				l.advance()
			}
		case ch == '"':
			if err := l.lexString(); err != nil {
				return err
			}
		case ch == '/':
			if err := l.lexRegex(); err != nil {
				return err
			}
		case ch == '{':
			if err := l.lexObject(); err != nil {
				return err
			}
		case isDigit(ch) || (ch == '-' && isDigit(l.peekRune(1))):
			if err := l.lexNumber(); err != nil {
				return err
			}
		case isIdentStart(ch):
			start := l.pos
			for l.pos < len(l.src) && isIdentChar(l.peekRune(0)) {
				l.advance()
			}
			l.emit(tokIdent, string(l.src[start:l.pos]), line, col)
		default:
			l.advance()
			switch ch {
			case '.':
				l.emit(tokDot, ".", line, col)
			case '[':
				l.depth++
				l.emit(tokLBracket, "[", line, col)
			case ']':
				if l.depth > 0 {
					l.depth--
				}
				l.emit(tokRBracket, "]", line, col)
			case '(':
				l.depth++
				l.emit(tokLParen, "(", line, col)
			case ')':
				if l.depth > 0 {
					l.depth--
				}
				l.emit(tokRParen, ")", line, col)
			case ',':
				l.emit(tokComma, ",", line, col)
			case '=':
				if l.peekRune(0) == '=' {
					l.advance()
					l.emit(tokEq, "==", line, col)
				} else {
					l.emit(tokEq, "=", line, col)
				}
			case '!':
				if l.peekRune(0) != '=' {
					return errorAt(line, col, `unexpected character "!"`)
				}
				l.advance()
				l.emit(tokNeq, "!=", line, col)
			case '<':
				if l.peekRune(0) == '=' {
					l.advance()
					l.emit(tokLte, "<=", line, col)
				} else {
					l.emit(tokLt, "<", line, col)
				}
			case '>':
				if l.peekRune(0) == '=' {
					l.advance()
					l.emit(tokGte, ">=", line, col)
				} else {
					l.emit(tokGt, ">", line, col)
				}
			default:
				return errorAt(line, col, "unexpected character %q", ch)
			}
		}
	}
	return nil
}

// lexString scans a double-quoted string with JSON escapes.
func (l *lexer) lexString() error {
	line, col := l.line, l.col
	start := l.pos
	l.advance() // opening quote
	for {
		if l.pos >= len(l.src) || l.peekRune(0) == '\n' {
			return errorAt(line, col, "unterminated string")
		}
		ch := l.advance()
		if ch == '\\' {
			if l.pos >= len(l.src) || l.peekRune(0) == '\n' {
				return errorAt(line, col, "unterminated string")
			}
			l.advance()
			continue
		}
		if ch == '"' {
			break
		}
	}
	raw := string(l.src[start:l.pos])
	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return errorAt(line, col, "invalid string literal: %v", err)
	}
	l.emit(tokString, decoded, line, col)
	return nil
}

// lexRegex scans /pattern/ where \/ escapes the delimiter. Any other escape
// is passed through unchanged to the Go regexp compiler.
func (l *lexer) lexRegex() error {
	line, col := l.line, l.col
	l.advance() // opening slash
	var pattern strings.Builder
	for {
		if l.pos >= len(l.src) || l.peekRune(0) == '\n' {
			return errorAt(line, col, "unterminated regex")
		}
		ch := l.advance()
		if ch == '\\' {
			if l.pos >= len(l.src) || l.peekRune(0) == '\n' {
				return errorAt(line, col, "unterminated regex")
			}
			next := l.advance()
			if next == '/' {
				pattern.WriteRune('/')
			} else {
				pattern.WriteRune('\\')
				pattern.WriteRune(next)
			}
			continue
		}
		if ch == '/' {
			break
		}
		pattern.WriteRune(ch)
	}
	l.emit(tokRegex, pattern.String(), line, col)
	return nil
}

// lexObject scans a balanced, string-aware {...} JSON object literal.
func (l *lexer) lexObject() error {
	line, col := l.line, l.col
	start := l.pos
	depth := 0
	inString := false
	for l.pos < len(l.src) {
		ch := l.advance()
		if inString {
			if ch == '\\' && l.pos < len(l.src) {
				l.advance()
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				raw := []byte(string(l.src[start:l.pos]))
				var compact bytes.Buffer
				if err := json.Compact(&compact, raw); err != nil || !json.Valid(raw) {
					return errorAt(line, col, "invalid JSON object literal")
				}
				l.emit(tokObject, compact.String(), line, col)
				return nil
			}
		}
	}
	return errorAt(line, col, "unterminated object literal")
}

func (l *lexer) lexNumber() error {
	line, col := l.line, l.col
	start := l.pos
	if l.peekRune(0) == '-' {
		l.advance()
	}
	for l.pos < len(l.src) {
		ch := l.peekRune(0)
		if isDigit(ch) || ch == '.' || ch == 'e' || ch == 'E' {
			l.advance()
			continue
		}
		if (ch == '+' || ch == '-') && (l.src[l.pos-1] == 'e' || l.src[l.pos-1] == 'E') {
			l.advance()
			continue
		}
		break
	}
	text := string(l.src[start:l.pos])
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return errorAt(line, col, "invalid number %q", text)
	}
	l.emit(tokNumber, text, line, col)
	return nil
}

// finish drops redundant newline tokens: leading, trailing, consecutive, and
// those preceding a continuation keyword. It appends the EOF token.
func (l *lexer) finish() []token {
	out := make([]token, 0, len(l.tokens)+1)
	for i, t := range l.tokens {
		if t.kind == tokNewline {
			if len(out) == 0 || out[len(out)-1].kind == tokNewline {
				continue
			}
			if i+1 < len(l.tokens) {
				next := l.tokens[i+1]
				if next.isKeyword(kwWhen) || next.isKeyword(kwAnd) || next.isKeyword(kwOr) {
					continue
				}
			}
		}
		out = append(out, t)
	}
	if len(out) > 0 && out[len(out)-1].kind == tokNewline {
		out = out[:len(out)-1]
	}
	eofLine, eofCol := l.line, l.col
	return append(out, token{kind: tokEOF, line: eofLine, col: eofCol})
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

func isIdentStart(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentChar(ch rune) bool {
	return isIdentStart(ch) || isDigit(ch) || ch == '-'
}
