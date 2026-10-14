// Package spl implements the Swap Policy Language ("spell"), a small
// declarative language for rewriting, validating and denying JSON requests
// before llama-swap forwards them upstream.
//
// A program is a list of clauses evaluated top to bottom:
//
//	ACTION [when CONDITION]
//
// Actions:
//
//	default <path> to <value>   set only when the path is missing
//	set <path> to <value>       always set
//	remove <path>               delete
//	apply <policy>              run a named policy from the Library
//	deny [status] "<message>"   reject the request (status defaults to 403)
//
// Conditions combine predicates with and, or, not and parentheses:
//
//	<path> = | != | < | <= | > | >= <scalar>
//	<path> matches /regex/
//	<path> is present | is missing
//	<path> in [<scalar>, ...]
//
// Paths address the JSON body (temperature, messages[-1].content), the
// request metadata bag (context.<key>), the API key (auth.key) and request
// facts (request.model, request.path, request.method, request.header.<name>).
// A negative index counts from the end of an array. Missing paths are absent:
// comparisons against them are false and "is missing" is true. A JSON null
// counts as present.
//
// Programs and Libraries are immutable once built and safe for concurrent use.
package spl
