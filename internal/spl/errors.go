package spl

import "fmt"

// Error is a parse or validation error located in the program source.
type Error struct {
	Line int
	Col  int
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("line %d:%d: %s", e.Line, e.Col, e.Msg)
}

func errorAt(line, col int, format string, args ...any) *Error {
	return &Error{Line: line, Col: col, Msg: fmt.Sprintf(format, args...)}
}
