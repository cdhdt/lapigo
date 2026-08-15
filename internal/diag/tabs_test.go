package diag_test

import (
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
)

func TestCheckNoTabs_NoTabsProducesNoDiagnostics(t *testing.T) {
	src := []byte("entities:\n  article:\n    sort: [-id]\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 0 {
		t.Fatalf("CheckNoTabs() = %v, want no diagnostics for a tab-free file", got)
	}
}

func TestCheckNoTabs_SpacesAreFine(t *testing.T) {
	src := []byte("    indented with spaces only\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 0 {
		t.Fatalf("CheckNoTabs() = %v, want no diagnostics; spaces are not tabs", got)
	}
}

func TestCheckNoTabs_SingleTabReportedAtCorrectPosition(t *testing.T) {
	src := []byte("entities:\n\tarticle:\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1: %+v", len(got), got)
	}
	d := got[0]
	if d.Pos.Line != 2 || d.Pos.Column != 1 {
		t.Fatalf("diagnostic position = %+v, want line 2 column 1", d.Pos)
	}
	if d.File != "lapigo.yaml" {
		t.Fatalf("diagnostic file = %q, want lapigo.yaml", d.File)
	}
	if d.Severity != diag.Error {
		t.Fatalf("diagnostic severity = %v, want Error: a tab is a hard failure, not advisory", d.Severity)
	}
	if d.Hint == "" {
		t.Fatalf("diagnostic has no hint; want an actionable fix telling the user to use spaces")
	}
	if !strings.Contains(strings.ToLower(d.Hint), "space") {
		t.Fatalf("hint = %q, want it to mention using spaces", d.Hint)
	}
}

func TestCheckNoTabs_OneDiagnosticPerTab(t *testing.T) {
	src := []byte("a:\t1\nb:\t2\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 2 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 2 (one per tab): %+v", len(got), got)
	}
	if got[0].Pos.Line != 1 || got[0].Pos.Column != 3 {
		t.Errorf("first tab position = %+v, want line 1 column 3 (after \"a:\")", got[0].Pos)
	}
	if got[1].Pos.Line != 2 || got[1].Pos.Column != 3 {
		t.Errorf("second tab position = %+v, want line 2 column 3 (after \"b:\")", got[1].Pos)
	}
}

func TestCheckNoTabs_MultipleTabsOnOneLine(t *testing.T) {
	src := []byte("a\tb\tc\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 2 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 2: %+v", len(got), got)
	}
	if got[0].Pos.Column != 2 {
		t.Errorf("first tab column = %d, want 2", got[0].Pos.Column)
	}
	if got[1].Pos.Column != 4 {
		t.Errorf("second tab column = %d, want 4", got[1].Pos.Column)
	}
}

// TestCheckNoTabs_ColumnIsRuneCountedNotByteCounted is the rune-vs-byte trap
// test applied to CheckNoTabs specifically: a multi-byte identifier before
// the tab must not shift the reported column by the identifier's extra byte
// count.
func TestCheckNoTabs_ColumnIsRuneCountedNotByteCounted(t *testing.T) {
	// "café" is 4 runes but 5 bytes (é is 2 bytes). A byte-counting
	// implementation would report column 6 for the tab; the correct,
	// rune-counting answer is column 5.
	src := []byte("café\tmore\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1: %+v", len(got), got)
	}
	if got[0].Pos.Column != 5 {
		t.Fatalf("tab column = %d, want 5 (rune count of \"café\" plus one, not the byte count)", got[0].Pos.Column)
	}
}

func TestCheckNoTabs_EndColumnCoversExactlyOneRune(t *testing.T) {
	src := []byte("a\tb\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1", len(got))
	}
	d := got[0]
	if d.EndColumn != d.Pos.Column+1 {
		t.Fatalf("EndColumn = %d, Pos.Column = %d; want EndColumn == Pos.Column+1 (one rune wide)", d.EndColumn, d.Pos.Column)
	}
}

func TestCheckNoTabs_EmptySourceProducesNoDiagnostics(t *testing.T) {
	got := diag.CheckNoTabs("lapigo.yaml", nil)
	if len(got) != 0 {
		t.Fatalf("CheckNoTabs() = %v, want no diagnostics for empty input", got)
	}
}

// TestCheckNoTabs_RendersWithoutPanicking is an integration test: the
// Diagnostics CheckNoTabs returns must survive a real Render call against
// the very source they were found in, including the tab character itself
// appearing in the line CheckNoTabs is complaining about.
func TestCheckNoTabs_RendersWithoutPanicking(t *testing.T) {
	src := []byte("entities:\n\tarticle:\n")
	ds := diag.CheckNoTabs("lapigo.yaml", src)
	if len(ds) == 0 {
		t.Fatalf("expected at least one diagnostic")
	}
	got := ds.Render(src)
	if !strings.Contains(got, "lapigo.yaml:2:1:") {
		t.Fatalf("Render() = %q, want a header for line 2 column 1", got)
	}
}
