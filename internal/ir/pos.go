// Package ir holds the intermediate representation: the validated, resolved
// form of a lapigo schema that templates render from.
//
// The IR is the boundary between input and output. Templates consume it and
// nothing else — never YAML, never a parser AST. A new input format produces
// the same IR and touches no template; a new output target consumes the same IR
// and touches no parser.
//
// This package must not import a YAML parser. Positions are carried as plain
// line and column numbers so the IR stays independent of how a schema was read.
package ir

// Pos is a location in a schema source file. Both fields are 1-based, and
// Column is counted in RUNES, not bytes.
//
// Counting in runes is not an implementation detail: a caret computed from a
// byte offset lands in the wrong place on any line containing a multi-byte
// character, and an accented identifier is enough to trigger it. Every
// conversion to bytes happens at render time and nowhere else.
//
// The zero Pos means "no position" — it belongs to values that were defaulted
// or synthesised rather than written by a human. A diagnostic pointing at the
// zero Pos is a bug in whoever built it.
type Pos struct {
	Line   int
	Column int
}

// IsZero reports whether p carries no position.
func (p Pos) IsZero() bool { return p.Line == 0 && p.Column == 0 }

// At pairs a value with the place in the schema where it was written.
//
// It is applied selectively, to the leaves that validation actually blames:
// identifiers, enum members, sort keys. Putting a Pos on every IR node instead
// would force a position onto defaulted values that have none, and would make
// every struct comparison in tests position-sensitive.
//
// At does make a naive structural comparison position-sensitive for the nodes
// that use it. Tests comparing IR trees must ignore Pos explicitly rather than
// discover this by surprise.
type At[T any] struct {
	Value T
	Pos   Pos
}

// NewAt pairs a value with a position.
func NewAt[T any](v T, p Pos) At[T] { return At[T]{Value: v, Pos: p} }

// Bare pairs a value with no position, for values lapigo synthesised itself.
func Bare[T any](v T) At[T] { return At[T]{Value: v} }
