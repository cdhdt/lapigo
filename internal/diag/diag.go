// Package diag is the diagnostic system every other lapigo package reports
// problems through. It defines one positioned problem type (Diagnostic), an
// accumulator that never stops at the first failure (Diagnostics), and a
// renderer that turns an accumulator into the compiler-grade error text
// described in CLAUDE.md and docs/superpowers/specs/2026-08-15-phase1-core-design.md
// §4.
//
// Shape is borrowed from go/scanner.Error and go/scanner.ErrorList: one
// struct per problem, a slice that accumulates and sorts, and an Error()
// method so the accumulator itself satisfies the standard error interface.
// What lapigo adds on top is an end column (so a diagnostic can underline a
// span, not just a point) and a Hint field (so every diagnostic can carry an
// actionable fix, per CLAUDE.md's "compiler-grade error messages" rule).
package diag

import (
	"sort"
	"strconv"
	"strings"

	"github.com/cdhdt/lapigo/internal/ir"
)

// Severity classifies whether a Diagnostic must stop generation or is
// advisory only. Its zero value is Error, so a Diagnostic built without
// explicitly setting Severity is treated as a hard failure rather than
// silently downgraded to a warning — the safer default.
type Severity int

const (
	// Error is a hard failure: generation must not proceed.
	Error Severity = iota
	// Warning is advisory: documented in spec §3.3 rule 5 (a mutable sort
	// key), it is reported but does not by itself stop generation.
	Warning
)

// String renders the severity the way it appears in a diagnostic header:
// "error" is never printed as a prefix (it's the default, unmarked case),
// only "warning" is, so String() exists for callers that want the word
// itself (logging, filtering) rather than the rendered form.
func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	default:
		return "severity(" + strconv.Itoa(int(s)) + ")"
	}
}

// Diagnostic is one positioned problem found in a schema file: a validator
// rejecting a bad sort spec, a tab character CheckNoTabs refuses to parse
// past, a wrapped goccy syntax error. Every validator in lapigo reports
// through this one type so a file with both a syntax typo and a semantic
// error prints both, in the same format, in one pass (spec §4.4).
type Diagnostic struct {
	// Severity distinguishes a hard failure from an advisory warning. Zero
	// value is Error.
	Severity Severity

	// File is the schema file the diagnostic refers to, e.g. "lapigo.yaml".
	File string

	// Pos is the start of the offending span. Pos.Column is 1-based and
	// counted in runes (see ir.Pos) — never bytes, so a caret computed from
	// it lands correctly even on a line with a multi-byte identifier.
	//
	// The zero Pos (ir.Pos{}) means "no position": a diagnostic that blames
	// something synthesised rather than written by a human. Render degrades
	// to a header with no line:col rather than printing a misleading ":0:0:".
	Pos ir.Pos

	// EndColumn is the 1-based rune column one past the last rune of the
	// span (exclusive), so span width is EndColumn - Pos.Column. A single
	// offending character therefore has EndColumn == Pos.Column + 1.
	//
	// EndColumn <= Pos.Column (unset, equal, or reversed) is not an error in
	// the caller: Render degrades it to a one-rune caret rather than
	// panicking or drawing nothing.
	EndColumn int

	// Message is the one-line description shown after "file:line:col: ".
	// It states what is wrong, not what to do about it — that's Hint.
	Message string

	// Hint is an actionable fix, indented under the caret when rendered. It
	// may contain embedded newlines for a multi-line hint; every line is
	// indented identically. Empty means no hint is shown for this
	// diagnostic — not every diagnostic has one worth stating.
	Hint string
}

// header renders the "file:line:col: [severity: ]message" line shared by
// both Diagnostics.Error() (the plain error text) and Render (the full,
// source-annotated report), so the two never drift apart.
func (d Diagnostic) header() string {
	var b strings.Builder
	b.WriteString(d.File)
	if !d.Pos.IsZero() {
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(d.Pos.Line))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(d.Pos.Column))
	}
	b.WriteString(": ")
	if d.Severity == Warning {
		b.WriteString("warning: ")
	}
	b.WriteString(d.Message)
	return b.String()
}

// Diagnostics accumulates Diagnostic values across one or more validation
// passes. Validators append to it and keep going — nothing stops at the
// first error, matching go/scanner.ErrorList's shape and spec §4.4's "one
// diagnostic type, accumulate, sort, print once".
//
// It is deliberately just a slice: callers may append directly
// (ds = append(ds, Diagnostic{...})) or use Add. Order of accumulation is
// preserved; sorting by position happens once, only when the accumulator is
// rendered (Error or Render), never as a side effect of accumulating.
type Diagnostics []Diagnostic

// Add appends one Diagnostic to the accumulator.
func (ds *Diagnostics) Add(d Diagnostic) {
	*ds = append(*ds, d)
}

// Err returns nil when ds is empty and ds itself (which satisfies error)
// otherwise. It exists so validation results compose with ordinary Go error
// handling: `if err := diags.Err(); err != nil { return err }`.
func (ds Diagnostics) Err() error {
	if len(ds) == 0 {
		return nil
	}
	return ds
}

// HasErrors reports whether ds contains at least one Error-severity
// diagnostic, as distinct from Err, which is non-nil even when ds holds
// nothing but warnings. Callers that must not proceed on warnings alone use
// Err; callers deciding whether to abort generation use HasErrors.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// sorted returns a stable-sorted copy of ds, ordered by position — line,
// then column — leaving ds itself untouched. Diagnostics with an equal
// position keep their relative accumulation order.
func (ds Diagnostics) sorted() Diagnostics {
	out := make(Diagnostics, len(ds))
	copy(out, ds)
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := out[i].Pos, out[j].Pos
		if pi.Line != pj.Line {
			return pi.Line < pj.Line
		}
		return pi.Column < pj.Column
	})
	return out
}

// Error renders every diagnostic in ds as one "file:line:col: message" line
// per diagnostic, sorted by position and joined by newlines, so ds satisfies
// the standard error interface without requiring the source bytes Render
// needs to draw a caret. Callers that want the full compiler-grade report —
// source line, caret, hint — call Render instead.
//
// Unlike go/scanner.ErrorList.Error, which truncates to "and N more errors",
// this lists every diagnostic: lapigo validators produce at most a handful
// per run, and a generic error string swallowing all-but-one would surprise
// a caller piping it into a log.
func (ds Diagnostics) Error() string {
	if len(ds) == 0 {
		return "no diagnostics"
	}
	sorted := ds.sorted()
	lines := make([]string, len(sorted))
	for i, d := range sorted {
		lines[i] = d.header()
	}
	return strings.Join(lines, "\n")
}
