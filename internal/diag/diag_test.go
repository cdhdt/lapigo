package diag_test

import (
	"errors"
	"math/rand"
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/source"
)

func TestDiagnostics_ErrIsNilWhenEmpty(t *testing.T) {
	var ds diag.Diagnostics
	if err := ds.Err(); err != nil {
		t.Fatalf("Err() on empty Diagnostics = %v, want nil", err)
	}
}

func TestDiagnostics_ErrIsNonNilWhenHoldingAnError(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
	}
	if err := ds.Err(); err == nil {
		t.Fatalf("Err() on Diagnostics holding an Error = nil, want non-nil")
	}
}

// TestDiagnostics_ErrIsNilForWarningsOnly is the regression test for the
// defect that made the idiomatic `if err := diags.Err(); err != nil { return
// err }` violate spec §3.3 rule 5: a warnings-only accumulator must not stop
// generation, so Err() must stay nil for it.
func TestDiagnostics_ErrIsNilForWarningsOnly(t *testing.T) {
	ds := diag.Diagnostics{
		{Severity: diag.Warning, File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "sort key is mutable"},
	}
	if err := ds.Err(); err != nil {
		t.Fatalf("Err() on warnings-only Diagnostics = %v, want nil", err)
	}
}

func TestDiagnostics_StrictErrIsNilWhenEmpty(t *testing.T) {
	var ds diag.Diagnostics
	if err := ds.StrictErr(); err != nil {
		t.Fatalf("StrictErr() on empty Diagnostics = %v, want nil", err)
	}
}

// TestDiagnostics_StrictErrIsNonNilForWarningsOnly is the -Werror accessor's
// whole reason to exist: unlike Err, it must fail even when every
// diagnostic is a Warning.
func TestDiagnostics_StrictErrIsNonNilForWarningsOnly(t *testing.T) {
	ds := diag.Diagnostics{
		{Severity: diag.Warning, File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "sort key is mutable"},
	}
	if err := ds.StrictErr(); err == nil {
		t.Fatalf("StrictErr() on warnings-only Diagnostics = nil, want non-nil")
	}
}

func TestDiagnostics_StrictErrIsNonNilWhenHoldingAnError(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
	}
	if err := ds.StrictErr(); err == nil {
		t.Fatalf("StrictErr() on Diagnostics holding an Error = nil, want non-nil")
	}
}

func TestDiagnostics_SatisfiesErrorInterface(t *testing.T) {
	var _ error = diag.Diagnostics{}
	// Also confirm Err() returns something usable through errors.As-style
	// plumbing: a plain error value wrapping our accumulator.
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
	}
	err := ds.Err()
	var target diag.Diagnostics
	if !errors.As(err, &target) {
		t.Fatalf("errors.As(err, *Diagnostics) failed; Err() should return the accumulator itself")
	}
	if len(target) != 1 {
		t.Fatalf("recovered Diagnostics has %d entries, want 1", len(target))
	}
}

func TestDiagnostic_DefaultSeverityIsError(t *testing.T) {
	d := diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "x"}
	if d.Severity != diag.Error {
		t.Fatalf("zero-value Diagnostic.Severity = %v, want diag.Error", d.Severity)
	}
}

func TestDiagnostics_Add(t *testing.T) {
	var ds diag.Diagnostics
	ds.Add(diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "one"})
	ds.Add(diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, Message: "two"})
	if len(ds) != 2 {
		t.Fatalf("len(ds) = %d, want 2", len(ds))
	}
}

func TestDiagnostics_ErrorListsEveryDiagnostic(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 5, Column: 1}, Message: "second"},
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "first"},
	}
	got := ds.Error()
	want := "lapigo.yaml:1:1: first\nlapigo.yaml:5:1: second"
	if got != want {
		t.Fatalf("Error() =\n%q\nwant\n%q", got, want)
	}
}

func TestDiagnostics_ErrorOnEmptyDoesNotPanic(t *testing.T) {
	var ds diag.Diagnostics
	_ = ds.Error() // must not panic
}

// --- HasErrors -------------------------------------------------------------
//
// HasErrors has zero test coverage before this file: it is the function
// callers use to decide whether to abort generation, so an inverted
// implementation would leave the rest of the suite green. These tests pin
// every boundary: no diagnostics, only errors, only warnings, a mix, and an
// out-of-range Severity value that must still count as an error.

func TestDiagnostics_HasErrors_EmptyIsFalse(t *testing.T) {
	var ds diag.Diagnostics
	if ds.HasErrors() {
		t.Fatalf("HasErrors() on empty Diagnostics = true, want false")
	}
}

func TestDiagnostics_HasErrors_TrueWithAnError(t *testing.T) {
	ds := diag.Diagnostics{{Severity: diag.Error, File: "f", Message: "m"}}
	if !ds.HasErrors() {
		t.Fatalf("HasErrors() with one Error diagnostic = false, want true")
	}
}

func TestDiagnostics_HasErrors_FalseWithOnlyWarnings(t *testing.T) {
	ds := diag.Diagnostics{
		{Severity: diag.Warning, File: "f", Message: "m1"},
		{Severity: diag.Warning, File: "f", Message: "m2"},
	}
	if ds.HasErrors() {
		t.Fatalf("HasErrors() with only Warning diagnostics = true, want false")
	}
}

func TestDiagnostics_HasErrors_TrueWithMixedSeverities(t *testing.T) {
	ds := diag.Diagnostics{
		{Severity: diag.Warning, File: "f", Message: "m1"},
		{Severity: diag.Error, File: "f", Message: "m2"},
	}
	if !ds.HasErrors() {
		t.Fatalf("HasErrors() with a warning and an error = false, want true")
	}
}

// TestDiagnostics_HasErrors_OutOfRangeSeverityCountsAsError is the specific
// mutant an adversarial review found surviving: a Diagnostic with
// Severity(7) (neither Error nor Warning) must count as an error, because
// HasErrors treats "not Warning" as the error condition, not "equals
// Error". Otherwise a future or corrupted severity value silently downgrades
// to advisory-only and generation proceeds on bad input.
func TestDiagnostics_HasErrors_OutOfRangeSeverityCountsAsError(t *testing.T) {
	ds := diag.Diagnostics{{Severity: diag.Severity(7), File: "f", Message: "m"}}
	if !ds.HasErrors() {
		t.Fatalf("HasErrors() with Severity(7) = false, want true (anything that is not Warning is an error)")
	}
	if ds.Err() == nil {
		t.Fatalf("Err() with Severity(7) = nil, want non-nil (Err and HasErrors must agree)")
	}
}

// --- Severity.String ---------------------------------------------------

func TestSeverity_String(t *testing.T) {
	cases := []struct {
		sev  diag.Severity
		want string
	}{
		{diag.Error, "error"},
		{diag.Warning, "warning"},
		{diag.Severity(7), "severity(7)"},
		{diag.Severity(-1), "severity(-1)"},
	}
	for _, tc := range cases {
		if got := tc.sev.String(); got != tc.want {
			t.Errorf("Severity(%d).String() = %q, want %q", int(tc.sev), got, tc.want)
		}
	}
}

// --- Total ordering ------------------------------------------------------

// TestDiagnostics_SortIsTotal exercises every tiebreak level of sorted's
// comparator directly through Error()'s output: two diagnostics sharing
// File, Line and Column (the old comparator's entire key) are still ordered
// deterministically by EndColumn, then Severity, then Message. The old
// comparator never exercised a column tiebreak at all -- every existing test
// used distinct lines -- so this pins File and Column ties specifically,
// not just Line ties.
func TestDiagnostics_SortIsTotal(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "b.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "b-file"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "a-file"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 5}, Message: "later-column"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 9, Message: "wider-span"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 3, Message: "narrower-span"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 3, Severity: diag.Warning, Message: "narrower-span"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 3, Message: "aaa-message"},
	}
	got := ds.Error()
	// Within File "a.yaml"/Line 1/Column 1, EndColumn breaks the tie first
	// (0 < 3 < 9: "a-file" has no EndColumn set, so it sorts before every
	// diagnostic that has one); within EndColumn 3, Severity breaks the tie
	// (Error < Warning); within EndColumn 3 and Severity Error, Message
	// breaks the tie ("aaa-message" < "narrower-span").
	want := "a.yaml:1:1: a-file\n" +
		"a.yaml:1:1: aaa-message\n" +
		"a.yaml:1:1: narrower-span\n" +
		"a.yaml:1:1: warning: narrower-span\n" +
		"a.yaml:1:1: wider-span\n" +
		"a.yaml:1:5: later-column\n" +
		"b.yaml:1:1: b-file"
	if got != want {
		t.Fatalf("Error() =\n%s\nwant\n%s", got, want)
	}
}

// TestDiagnostics_SortIsDeterministicAcrossShuffles is the mutation-proof
// version of the ordering guarantee: sorting must not depend on
// accumulation order (spec §4.5). Many random shuffles of the same set of
// diagnostics -- including several that collide on File, Line and Column,
// so ties actually get exercised -- must all render identically.
func TestDiagnostics_SortIsDeterministicAcrossShuffles(t *testing.T) {
	base := []diag.Diagnostic{
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "alpha"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Severity: diag.Warning, Message: "alpha"},
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 5, Message: "beta"},
		{File: "a.yaml", Pos: source.Pos{Line: 2, Column: 3}, EndColumn: 4, Message: "gamma"},
		{File: "b.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "delta"},
		{File: "b.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "epsilon"},
	}

	var want string
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		shuffled := make(diag.Diagnostics, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		got := shuffled.Render(
			source.File{Name: "a.yaml", Src: []byte("line one\nline two\n")},
			source.File{Name: "b.yaml", Src: []byte("line one\n")},
		)
		if trial == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("trial %d: Render() differs after shuffling accumulation order\ngot:\n%s\nwant (from trial 0):\n%s", trial, got, want)
		}
	}
}

// --- errors.Is / errors.As reflexivity and unwrapping ---------------------

// TestDiagnostics_ErrorsIsIsReflexive is the regression test for the
// defect an adversarial review found: errors.Is(err, err) was false for a
// Diagnostics error, because Diagnostics is a slice (uncomparable with ==)
// and errors.Is checks comparability before it ever tries ==, so without an
// explicit Is method the comparison was silently skipped rather than
// panicking. Reflexivity is the one property every caller of an error value
// is entitled to assume.
func TestDiagnostics_ErrorsIsIsReflexive(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
		{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 3}, Message: "also boom"},
	}
	err := ds.Err()
	if !errors.Is(err, err) {
		t.Fatalf("errors.Is(err, err) = false, want true")
	}
}

func TestDiagnostics_ErrorsIsDistinguishesDifferentContent(t *testing.T) {
	a := diag.Diagnostics{{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"}}.Err()
	b := diag.Diagnostics{{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "different"}}.Err()
	if errors.Is(a, b) {
		t.Fatalf("errors.Is(a, b) = true for Diagnostics with different content, want false")
	}
}

func TestDiagnostics_IsDistinguishesDifferentLength(t *testing.T) {
	shorter := diag.Diagnostics{{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"}}
	longer := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
		{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, Message: "also boom"},
	}
	if shorter.Is(longer.Err()) {
		t.Fatalf("Is() = true for Diagnostics of different length, want false")
	}
}

// TestDiagnostics_IsFalseForAnUnrelatedError confirms Is only ever matches
// another Diagnostics value: errors.Is(err, someOtherErrorType) must not
// panic on the failed type assertion and must simply report no match.
func TestDiagnostics_IsFalseForAnUnrelatedError(t *testing.T) {
	ds := diag.Diagnostics{{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"}}
	if ds.Is(errors.New("some other error")) {
		t.Fatalf("Is() = true for an unrelated error type, want false")
	}
	if errors.Is(ds.Err(), errors.New("some other error")) {
		t.Fatalf("errors.Is(ds.Err(), unrelated) = true, want false")
	}
}

// TestDiagnostics_UnwrapExposesEachDiagnostic confirms a caller can reach
// one specific accumulated Diagnostic through errors.As, which is what
// "was there a tab error?" reaches for rather than re-parsing Error()'s
// joined string.
func TestDiagnostics_UnwrapExposesEachDiagnostic(t *testing.T) {
	tabErr := diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, Message: "tab character is not allowed in schema files"}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "unrelated"},
		tabErr,
	}
	err := ds.Err()

	var found diag.Diagnostic
	ok := false
	for _, e := range ds.Unwrap() {
		if d, isD := e.(diag.Diagnostic); isD && d == tabErr {
			found = d
			ok = true
		}
	}
	if !ok {
		t.Fatalf("Unwrap() did not expose the tab diagnostic")
	}
	if found.Message != tabErr.Message {
		t.Fatalf("unwrapped diagnostic message = %q, want %q", found.Message, tabErr.Message)
	}

	// And the Diagnostic itself must behave as a normal error.
	var asErr error = tabErr
	if asErr.Error() != "lapigo.yaml:2:1: tab character is not allowed in schema files" {
		t.Fatalf("Diagnostic.Error() = %q, want the same text as its header", asErr.Error())
	}
	_ = err // ds.Err() constructed above only to mirror real call sites
}

// --- FromAt ----------------------------------------------------------------

func TestFromAt_FillsPosAndEndColumnFromTheSpan(t *testing.T) {
	span := source.NewAt("created_at", source.Pos{Line: 12, Column: 12}, source.Pos{Line: 12, Column: 23})
	d := diag.FromAt("lapigo.yaml", span, diag.Warning, `sort key "created_at" is mutable`, "mark it unique or drop it from sort")

	want := diag.Diagnostic{
		Severity:  diag.Warning,
		File:      "lapigo.yaml",
		Pos:       source.Pos{Line: 12, Column: 12},
		EndColumn: 23,
		Message:   `sort key "created_at" is mutable`,
		Hint:      "mark it unique or drop it from sort",
	}
	if d != want {
		t.Fatalf("FromAt() = %+v, want %+v", d, want)
	}
}

func TestFromAt_BareValueProducesInvalidPos(t *testing.T) {
	span := source.Bare("id")
	d := diag.FromAt("lapigo.yaml", span, diag.Error, "synthesised id column", "")
	if d.Pos.IsValid() {
		t.Fatalf("FromAt() on a Bare value produced a valid Pos %+v, want an invalid one (Bare carries no position)", d.Pos)
	}
}
