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

	"github.com/cdhdt/lapigo/internal/source"
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
//
// Any value other than Error or Warning cannot occur from code in this
// package, but String must still answer for it rather than panic or claim
// it is one of the two known severities: HasErrors treats such a value as
// an error (see its doc comment), and this method's output should not
// contradict that by rendering it as if it were fine.
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
	// Render looks a diagnostic's source up by this name (see Render).
	File string

	// Pos is the start of the offending span. Pos.Column is 1-based and
	// counted in runes (see source.Pos) — never bytes, so a caret computed
	// from it lands on the right character even on a line with a
	// multi-byte identifier. It is not, however, guaranteed to land on the
	// right terminal *cell*: see Render's doc comment on display width.
	//
	// A Pos for which Pos.IsValid() is false — the zero Pos, but also any
	// Pos with a non-positive Line or Column, such as one a buggy caller
	// hand-built as source.Pos{Line: 0, Column: 5} — means "no position": a
	// diagnostic that blames something synthesised rather than written by a
	// human (see source.Bare). Render degrades to a header with no
	// "line:col" segment rather than printing the misleading "file.yaml:0:5:"
	// or "file.yaml:-3:-9:" that guarding on Pos.IsZero() alone would let
	// through for those non-zero-but-still-meaningless positions.
	//
	// This is diag's own contract for what "no position" means, chosen
	// because a diagnostic has to render *something* even when handed a
	// position nobody meant to be real. It does not restate source.Pos's own
	// framing of the zero value (there, a zero Pos documents a bug in
	// whoever built the value it's attached to) — that is a different
	// package's concern, not this one's.
	Pos source.Pos

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

// Error satisfies the standard error interface for one Diagnostic, so
// Diagnostics.Unwrap can hand each accumulated entry back as a plain error a
// caller can pull out with errors.As, rather than forcing every caller to
// re-parse Diagnostics.Error's joined string to find one specific problem.
// It renders the same text as the header line Render prints above the
// source snippet.
func (d Diagnostic) Error() string { return d.header() }

// FromAt builds a Diagnostic for the span recorded by a source.At[string] —
// the shape a validator gets back for an identifier, enum member, or sort
// key it read out of the schema. At carries both where the span starts and
// where it ends (source.At's doc comment), so this is the arithmetic every
// call site would otherwise repeat by hand as
// Diagnostic{File: f, Pos: x.Pos, EndColumn: x.End.Column}.
//
// The span is assumed to lie on a single line: span.End.Line is not
// consulted, only span.End.Column. Every phase 1 use — identifiers, enum
// members, sort keys — is a single-line token; a genuinely multi-line span
// would need a richer Diagnostic than exists today.
func FromAt(file string, span source.At[string], severity Severity, message, hint string) Diagnostic {
	return Diagnostic{
		Severity:  severity,
		File:      file,
		Pos:       span.Pos,
		EndColumn: span.End.Column,
		Message:   message,
		Hint:      hint,
	}
}

// header renders the "file:line:col: [severity: ]message" line shared by
// both Diagnostics.Error() (the plain error text) and Render (the full,
// source-annotated report), so the two never drift apart.
func (d Diagnostic) header() string {
	var b strings.Builder
	b.WriteString(d.File)
	if d.Pos.IsValid() {
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
//
// Diagnostics carries the same typed-nil hazard every named error slice
// type does: `var e error = Diagnostics(nil)` produces a non-nil error
// interface value, because the interface has a concrete type even though
// the slice value inside it is nil. `Diagnostics(nil) == nil` is true;
// `error(Diagnostics(nil)) == nil` is false. Never assign a possibly-nil
// Diagnostics directly to a bare error return; use Err() or StrictErr(),
// both of which return a literal, interface-level nil for the empty case.
type Diagnostics []Diagnostic

// Add appends one Diagnostic to the accumulator.
func (ds *Diagnostics) Add(d Diagnostic) {
	*ds = append(*ds, d)
}

// Err reports whether ds contains at least one Error-severity diagnostic
// (see HasErrors). It returns nil both when ds is empty and when it holds
// only Warning diagnostics, so the idiomatic
//
//	if err := diags.Err(); err != nil { return err }
//
// does not abort generation on warnings alone — required by spec §3.3 rule
// 5, which defines a warning (a mutable sort key) that must not by itself
// stop generation. A caller that wants strict "-Werror" behaviour, where
// even a warnings-only accumulator is a failure, uses StrictErr instead.
func (ds Diagnostics) Err() error {
	if !ds.HasErrors() {
		return nil
	}
	return ds
}

// StrictErr returns nil only when ds is completely empty, and ds itself
// otherwise — including when every diagnostic in it is a Warning. This is
// the -Werror accessor: a caller that wants to fail the build on any
// diagnostic at all, not just hard errors, calls StrictErr rather than Err.
// Most callers want Err, not this: it exists because "should warnings fail
// the build" is a caller decision, not one diag should make unilaterally by
// only exposing one behaviour.
func (ds Diagnostics) StrictErr() error {
	if len(ds) == 0 {
		return nil
	}
	return ds
}

// HasErrors reports whether ds contains at least one diagnostic whose
// Severity is not Warning. That includes the defined Error value, and also
// any out-of-range Severity nothing in this package currently produces:
// treating an unrecognised severity as advisory-by-default would let a
// future severity value silently downgrade what should be a hard failure
// into one HasErrors — the function every caller uses to decide whether to
// abort generation — quietly ignores. "Not a Warning" is the safe direction
// to default an unknown value toward; "not an Error" is not.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Severity != Warning {
			return true
		}
	}
	return false
}

// sorted returns a stable-sorted copy of ds, ordered by File, then Line,
// Column, EndColumn, Severity, and finally Message. This is a total order
// over every field on Diagnostic except Hint (prose, not identity), chosen
// so two diagnostics that happen to share a file and position — including
// two diagnostics in *different* files that share a line and column, which
// Line/Column alone cannot distinguish — still sort the same way regardless
// of the order validators appended them in. Accumulation order is an
// artifact of traversal order over a map or a tree walk, and CLAUDE.md's
// first architecture rule requires byte-identical output for identical
// input (spec §4.5); a comparator that stops at Column leaves ties broken
// by accumulation order, which is not deterministic across runs.
func (ds Diagnostics) sorted() Diagnostics {
	out := make(Diagnostics, len(ds))
	copy(out, ds)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line < b.Pos.Line
		}
		if a.Pos.Column != b.Pos.Column {
			return a.Pos.Column < b.Pos.Column
		}
		if a.EndColumn != b.EndColumn {
			return a.EndColumn < b.EndColumn
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		return a.Message < b.Message
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
// a caller piping it into a log. (CheckNoTabs, the one validator that can
// produce many diagnostics from one file, caps itself — see its own doc
// comment — so this method never needs to.)
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

// Is reports whether target is a Diagnostics holding the same sequence of
// diagnostics as ds, one element at a time. Its purpose is to make
// errors.Is(err, err) — the one property every caller of an error value is
// entitled to assume — return true for an error produced by Err or
// StrictErr.
//
// Without it, errors.Is(err, err) is false for this type: Diagnostics is a
// slice, slices are not comparable with ==, and errors.Is checks
// comparability before it ever attempts err == target, silently skipping
// straight to Unwrap/Is-method matching instead of panicking. Is supplies
// that method. The element-wise comparison it does is well-defined and
// panic-free because every Diagnostic field is itself a plain comparable
// value — no slice, map or func — so Diagnostic itself supports ==.
func (ds Diagnostics) Is(target error) bool {
	other, ok := target.(Diagnostics)
	if !ok {
		return false
	}
	if len(ds) != len(other) {
		return false
	}
	for i := range ds {
		if ds[i] != other[i] {
			return false
		}
	}
	return true
}

// Unwrap exposes each accumulated Diagnostic as an error — Diagnostic
// implements error via its own Error() method — so errors.As(err, &target)
// can pull one specific diagnostic out of an accumulator (a caller asking
// "was there a tab error?" reaches for exactly this), and errors.Is can
// match against an individual Diagnostic wrapped elsewhere with fmt.Errorf's
// %w. This is the standard library's own multi-error convention
// (errors.Join produces the same Unwrap() []error shape), so Diagnostics
// composes with errors.Is/As without lapigo inventing a parallel mechanism.
func (ds Diagnostics) Unwrap() []error {
	errs := make([]error, len(ds))
	for i, d := range ds {
		errs[i] = d
	}
	return errs
}
