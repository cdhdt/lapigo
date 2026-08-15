package ir

import "fmt"

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

// Filter is one whitelisted equality filter on an Entity (spec §3.4). A
// request selects which whitelisted column to filter on; it can never name
// one. Field is a resolved *Field pointer, not a name, for the same reason
// as SortKey.Field: a template needs the column, Go type and nullability
// without performing a lookup.
type Filter struct {
	Field *Field
	Op    FilterOp
}
