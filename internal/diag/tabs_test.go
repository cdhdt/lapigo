package diag_test

import (
	"fmt"
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/source"
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

// TestCheckNoTabs_SingleTabExact pins the full Diagnostic value for the
// single most common case — one tab, one line — so a mutation to any field
// (severity, position, end column, message text, or hint text) fails a
// direct struct comparison instead of the loose field-by-field Contains
// checks this test used to make. Reducing the hint to the single word
// "space", for example, used to pass; comparing the whole Hint string does
// not.
func TestCheckNoTabs_SingleTabExact(t *testing.T) {
	src := []byte("entities:\n\tarticle:\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)

	want := diag.Diagnostics{
		{
			Severity:  diag.Error,
			File:      "lapigo.yaml",
			Pos:       source.Pos{Line: 2, Column: 1},
			EndColumn: 2,
			Message:   "tab character is not allowed in schema files",
			Hint: "use spaces instead of tabs; YAML forbids tabs for indentation, and " +
				"lapigo forbids them everywhere else so reported column numbers stay correct",
		},
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("CheckNoTabs() =\n%+v\nwant\n%+v", got, want)
	}
}

// TestCheckNoTabs_OneDiagnosticPerLineNotPerTab is the regression test for
// the defect an adversarial review found: CheckNoTabs used to emit one
// diagnostic per *tab*, so a 500-line tab-indented file produced a report
// hundreds of kilobytes long, repeating the same hint every time. It now
// collapses every tab on a line into one diagnostic, with the message
// stating the count when there is more than one. Two tabs on the *same*
// line collapse to one diagnostic; two tabs on *different* lines still
// produce two, since each line gets its own diagnostic.
func TestCheckNoTabs_OneDiagnosticPerLineNotPerTab(t *testing.T) {
	t.Run("two tabs on different lines: two diagnostics", func(t *testing.T) {
		src := []byte("a:\t1\nb:\t2\n")
		got := diag.CheckNoTabs("lapigo.yaml", src)
		if len(got) != 2 {
			t.Fatalf("CheckNoTabs() returned %d diagnostics, want 2 (one per line): %+v", len(got), got)
		}
		if got[0].Pos.Line != 1 || got[0].Pos.Column != 3 || got[0].Message != "tab character is not allowed in schema files" {
			t.Errorf("first diagnostic = %+v, want line 1 column 3, singular message", got[0])
		}
		if got[1].Pos.Line != 2 || got[1].Pos.Column != 3 || got[1].Message != "tab character is not allowed in schema files" {
			t.Errorf("second diagnostic = %+v, want line 2 column 3, singular message", got[1])
		}
	})

	t.Run("two tabs on the same line: one diagnostic with a count", func(t *testing.T) {
		src := []byte("a\tb\tc\n")
		got := diag.CheckNoTabs("lapigo.yaml", src)
		if len(got) != 1 {
			t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1 (tabs collapsed per line): %+v", len(got), got)
		}
		want := diag.Diagnostic{
			Severity:  diag.Error,
			File:      "lapigo.yaml",
			Pos:       source.Pos{Line: 1, Column: 2}, // first tab, after "a"
			EndColumn: 3,
			Message:   "2 tab characters are not allowed in schema files",
			Hint: "use spaces instead of tabs; YAML forbids tabs for indentation, and " +
				"lapigo forbids them everywhere else so reported column numbers stay correct",
		}
		if got[0] != want {
			t.Fatalf("CheckNoTabs()[0] =\n%+v\nwant\n%+v", got[0], want)
		}
	})
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
	// And pinned absolutely, not just relationally: a mutant that shifts
	// both Pos.Column and EndColumn by the same wrong amount passes the
	// relational check above but not this one.
	if d.Pos.Column != 2 || d.EndColumn != 3 {
		t.Fatalf("diagnostic position = {Column:%d EndColumn:%d}, want {Column:2 EndColumn:3}", d.Pos.Column, d.EndColumn)
	}
}

func TestCheckNoTabs_EmptySourceProducesNoDiagnostics(t *testing.T) {
	got := diag.CheckNoTabs("lapigo.yaml", nil)
	if len(got) != 0 {
		t.Fatalf("CheckNoTabs() = %v, want no diagnostics for empty input", got)
	}
}

// TestCheckNoTabs_RendersExactly is an integration test: the Diagnostics
// CheckNoTabs returns must render, against the very source they were found
// in, into the exact compiler-grade report a user would see — including the
// tab character itself being replaced by a space in the printed line (spec
// §4.5 / render.go's sanitizeLine).
func TestCheckNoTabs_RendersExactly(t *testing.T) {
	src := []byte("entities:\n\tarticle:\n")
	ds := diag.CheckNoTabs("lapigo.yaml", src)
	if len(ds) != 1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1", len(ds))
	}
	got := ds.Render(source.File{Name: "lapigo.yaml", Src: src})
	want := "lapigo.yaml:2:1: tab character is not allowed in schema files\n" +
		"   2 |  article:\n" +
		"     | ^\n" +
		"   use spaces instead of tabs; YAML forbids tabs for indentation, and " +
		"lapigo forbids them everywhere else so reported column numbers stay correct"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestCheckNoTabs_LoneCarriageReturnIsALineBreak is the regression test for
// the defect where CheckNoTabs only advanced its line counter on '\n':
// "a: 1\rb:\t2\rc: 3\r" is three lines under YAML 1.2 (a lone '\r' is a line
// break), so the tab must be reported on line 2, column 3 — not line 1,
// column 8, which is what counting only '\n' as a break produces.
func TestCheckNoTabs_LoneCarriageReturnIsALineBreak(t *testing.T) {
	src := []byte("a: 1\rb:\t2\rc: 3\r")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1: %+v", len(got), got)
	}
	if got[0].Pos.Line != 2 || got[0].Pos.Column != 3 {
		t.Fatalf("tab position = %+v, want line 2 column 3 (lone '\\r' must count as a line break)", got[0].Pos)
	}
}

// TestCheckNoTabs_TabAfterCRLFReportsTheRightLine confirms CheckNoTabs's own
// line-break scanning treats "\r\n" as a single break, matching splitLines
// in render.go, when a tab occurs on the line right after one.
func TestCheckNoTabs_TabAfterCRLFReportsTheRightLine(t *testing.T) {
	src := []byte("a: 1\r\nb:\t2\r\n")
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1: %+v", len(got), got)
	}
	if got[0].Pos.Line != 2 || got[0].Pos.Column != 3 {
		t.Fatalf("tab position = %+v, want line 2 column 3 (\"\\r\\n\" must count as one line break)", got[0].Pos)
	}
}

// TestCheckNoTabs_LeadingBOMDoesNotShiftColumn confirms a leading
// byte-order mark is stripped before column counting starts, matching what
// a real YAML parser does — otherwise the tab on line 1 would be reported
// one column further right than the parser's own diagnostics would agree
// with.
func TestCheckNoTabs_LeadingBOMDoesNotShiftColumn(t *testing.T) {
	src := append([]byte("\xef\xbb\xbf"), []byte("a:\t1\n")...)
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != 1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want 1: %+v", len(got), got)
	}
	if got[0].Pos.Line != 1 || got[0].Pos.Column != 3 {
		t.Fatalf("tab position = %+v, want line 1 column 3 (the BOM must not occupy a column)", got[0].Pos)
	}
}

// TestCheckNoTabs_CapsAndSummarizesLargeFiles is the regression test for
// the defect where a 500-line tab-indented file produced a 121 KB render
// with the same hint repeated 500 times. Past a cap, CheckNoTabs stops
// producing one diagnostic per offending line and instead appends a single
// summary diagnostic — go/scanner's own habit of truncating a long error
// list with "and N more" rather than printing all of it.
func TestCheckNoTabs_CapsAndSummarizesLargeFiles(t *testing.T) {
	const totalOffendingLines = 15
	var src []byte
	for i := 0; i < totalOffendingLines; i++ {
		src = append(src, []byte(fmt.Sprintf("k%d:\tv\n", i))...)
	}

	got := diag.CheckNoTabs("lapigo.yaml", src)

	const tabCap = 10 // matches maxTabDiagnostics in tabs.go
	if len(got) != tabCap+1 {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want %d (cap + one summary): %+v", len(got), tabCap+1, got)
	}
	for i := 0; i < tabCap; i++ {
		if got[i].Pos.Line != i+1 {
			t.Errorf("diagnostic %d is for line %d, want line %d", i, got[i].Pos.Line, i+1)
		}
		if !got[i].Pos.IsValid() {
			t.Errorf("diagnostic %d has an invalid position, want a real one", i)
		}
	}
	summary := got[tabCap]
	if summary.Pos.IsValid() {
		t.Errorf("summary diagnostic has a position %+v, want none (it does not blame one line)", summary.Pos)
	}
	wantMsg := fmt.Sprintf("and %d more line(s) with tab characters (showing the first %d)", totalOffendingLines-tabCap, tabCap)
	if summary.Message != wantMsg {
		t.Fatalf("summary message = %q, want %q", summary.Message, wantMsg)
	}
}

// TestCheckNoTabs_UnderTheCapProducesNoSummary confirms the summary
// diagnostic only appears once the cap is actually exceeded — a file with
// exactly maxTabDiagnostics offending lines must not get a spurious "and 0
// more" entry.
func TestCheckNoTabs_UnderTheCapProducesNoSummary(t *testing.T) {
	const totalOffendingLines = 10
	var src []byte
	for i := 0; i < totalOffendingLines; i++ {
		src = append(src, []byte(fmt.Sprintf("k%d:\tv\n", i))...)
	}
	got := diag.CheckNoTabs("lapigo.yaml", src)
	if len(got) != totalOffendingLines {
		t.Fatalf("CheckNoTabs() returned %d diagnostics, want exactly %d (no summary entry when the cap is not exceeded)", len(got), totalOffendingLines)
	}
}
