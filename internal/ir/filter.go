package ir

import (
	"fmt"

	"github.com/cdhdt/lapigo/internal/source"
)

// FilterOp is a comparison operator usable in a Filter. It is defined as a
// closed enumeration now, even though phase 1 supports exactly one member
// (spec §3.4), so that a later phase adding operators extends this type
// instead of turning Filter.Op into a string that templates would compare
// against ad hoc.
type FilterOp int

// FilterOpEq is equality — the only operator spec §3.4 supports in phase 1.
const FilterOpEq FilterOp = iota

func (op FilterOp) String() string {
	switch op {
	case FilterOpEq:
		return "eq"
	default:
		return fmt.Sprintf("FilterOp(%d)", int(op))
	}
}

// SQL returns the SQL comparison operator op renders as in a WHERE clause,
// e.g. "=" for FilterOpEq. This is distinct from String, which returns the
// diagnostic keyword ("eq"): a WHERE-clause renderer needs op's SQL spelling
// without hardcoding the single-operator assumption FilterOp exists as a
// closed enum to prevent, ready for the day a second operator exists to
// choose between.
func (op FilterOp) SQL() string {
	switch op {
	case FilterOpEq:
		return "="
	default:
		return fmt.Sprintf("<unknown FilterOp %d>", int(op))
	}
}

// Filter is one whitelisted equality filter on an Entity (spec §3.4). A
// request selects which whitelisted column to filter on; it can never name
// one. Field is a resolved *Field pointer, not a name, for the same reason
// as SortKey.Field: a template needs the column, Go type and nullability
// without performing a lookup.
//
// Span is the `filters:` list entry itself, not the filtered field's own
// declaration — an earlier revision carried no position at all here, so a
// diagnostic about a bad filter pointed at the field's `fields:` entry
// instead of the offending line in `filters:` (spec §2.2).
type Filter struct {
	Field *Field
	Op    FilterOp
	Span  source.Span
}
