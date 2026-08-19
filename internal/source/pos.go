// Package source holds the vocabulary for talking about schema source files:
// positions, values paired with the place they were written, and the files
// themselves.
//
// It is deliberately a leaf: it imports nothing outside the standard library
// and depends on no other lapigo package. Both the intermediate representation
// and the diagnostic renderer need to name a position, and neither should have
// to import the other to do it. An earlier layout put Pos in the ir package,
// which meant the diagnostic layer depended on the IR and the IR could never
// report a diagnostic without an import cycle.
//
// Nothing here knows how a schema was read. Positions are plain line and
// column numbers, so swapping the YAML parser touches no consumer.
package source

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

// IsZero reports whether p carries no position at all.
func (p Pos) IsZero() bool { return p.Line == 0 && p.Column == 0 }

// IsValid reports whether p can address a real character: both coordinates are
// 1-based, so anything below 1 is meaningless.
//
// IsZero is not enough on its own. A Pos of {Line: 0, Column: 5} is not zero,
// yet rendering it produces the header "file.yaml:0:5:" — exactly the
// misleading output that suppressing the zero Pos exists to avoid. Consumers
// guard on IsValid, not IsZero.
func (p Pos) IsValid() bool { return p.Line >= 1 && p.Column >= 1 }

// Span is a half-open range in a schema source file: End is exclusive, so a
// single-character span has End.Column == Start.Column+1.
//
// Span exists for the IR nodes that have a place in the source but no value of
// their own to pair with it — an entity's name, a sort key, a filter entry, a
// relation. Those carried no position at all in an earlier revision, so a
// diagnostic about a filter pointed at the filtered field's declaration rather
// than at the offending `filters:` entry, sending the reader to the wrong line.
// Reconstructing a span downstream is guesswork: only the parser saw the
// written form, and a quoted or signed token is wider than the value it
// carries.
type Span struct {
	Start Pos
	End   Pos
}

// IsValid reports whether s addresses real characters.
func (s Span) IsValid() bool { return s.Start.IsValid() }

// NewSpan builds a span from its two ends.
func NewSpan(start, end Pos) Span { return Span{Start: start, End: end} }

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
// At carries an End as well as a Pos. Recomputing the span at each call site as
// Pos.Column + utf8.RuneCountInString(Value) is wrong for any quoted scalar:
// "created_at" occupies twelve columns on the line while its value is ten
// runes, so every caret over a quoted key would fall two columns short. Only
// the parser knows how wide the written form was, so only the parser can
// record it.
type At[T any] struct {
	Value T
	Pos   Pos
	End   Pos // exclusive; the position just past the written form
}

// NewAt pairs a value with the span it occupied in the source.
func NewAt[T any](v T, start, end Pos) At[T] { return At[T]{Value: v, Pos: start, End: end} }

// Span returns a's extent, so a caller holding either an At or a Span can be
// written the same way.
func (a At[T]) Span() Span { return Span{Start: a.Pos, End: a.End} }

// Bare pairs a value with no position, for values lapigo synthesised itself
// rather than read from a schema — a defaulted table name, an implied endpoint.
// A diagnostic blaming a Bare value has nowhere to point, which means it is
// blaming the wrong thing.
func Bare[T any](v T) At[T] { return At[T]{Value: v} }

// File is a schema source file: the name a diagnostic should print, and the
// bytes to quote from.
//
// Diagnostics carry a file name, so rendering takes a set of files and looks
// each one up. A renderer given a single source renders every diagnostic
// against it whatever file the diagnostic names, printing a line from one file
// beneath a header naming another.
type File struct {
	Name string
	Src  []byte
}
