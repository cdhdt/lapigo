package diag_test

import (
	"errors"
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

func TestDiagnostics_ErrIsNonNilWhenNonEmpty(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
	}
	if err := ds.Err(); err == nil {
		t.Fatalf("Err() on non-empty Diagnostics = nil, want non-nil")
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
