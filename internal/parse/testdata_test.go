package parse

import (
	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// stripPositions zeroes every source.Pos this package's tests do not want to
// pin down exactly, mutating s in place. It exists so a golden-schema test
// can assert on every other field of the IR with reflect.DeepEqual without
// hand-computing the column of every field name in a large fixture --
// CLAUDE.md's own note on At[T] breaking a naive structural comparison
// applies here exactly as it does to internal/ir's own tests, and this is
// this package's equivalent of the cmpopts.IgnoreTypes(source.Pos{}) option
// the spec names, written without adding a new dependency (see CLAUDE.md:
// "dependencies are a liability").
//
// Tests that care about exact positions (this package's whole reason to
// exist) do not use this helper: they assert Pos and End directly.
func stripPositions(s *ir.Schema) {
	if s == nil {
		return
	}
	for _, e := range s.Entities {
		e.NameSpan, e.TableSpan = source.Span{}, source.Span{}
		for _, f := range e.Fields {
			f.Name.Pos, f.Name.End = source.Pos{}, source.Pos{}
			for i := range f.EnumValues {
				f.EnumValues[i].Pos, f.EnumValues[i].End = source.Pos{}, source.Pos{}
			}
		}
		for i := range e.Sort.Keys {
			e.Sort.Keys[i].Span = source.Span{}
		}
		for i := range e.Filters {
			e.Filters[i].Span = source.Span{}
		}
		for i := range e.Indexes {
			for j := range e.Indexes[i].Columns {
				e.Indexes[i].Columns[j].Span = source.Span{}
			}
		}
		for i := range e.Relations {
			e.Relations[i].NameSpan, e.Relations[i].TargetSpan = source.Span{}, source.Span{}
		}
	}
}
