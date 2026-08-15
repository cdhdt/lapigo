package source

import "testing"

func TestPos_IsZero(t *testing.T) {
	tests := []struct {
		name string
		p    Pos
		want bool
	}{
		{"zero value", Pos{}, true},
		{"line and column both explicitly zero", Pos{Line: 0, Column: 0}, true},
		{"line set, column zero", Pos{Line: 1, Column: 0}, false},
		{"column set, line zero", Pos{Line: 0, Column: 1}, false},
		{"both set", Pos{Line: 1, Column: 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPos_IsValid(t *testing.T) {
	tests := []struct {
		name string
		p    Pos
		want bool
	}{
		{"zero Pos", Pos{}, false},
		{"minimum valid position", Pos{Line: 1, Column: 1}, true},
		{"ordinary position", Pos{Line: 12, Column: 12}, true},
		{"line below 1, column positive: not zero, still invalid", Pos{Line: 0, Column: 5}, false},
		{"column below 1, line positive", Pos{Line: 5, Column: 0}, false},
		{"negative line", Pos{Line: -1, Column: 1}, false},
		{"negative column", Pos{Line: 1, Column: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPos_ZeroButNotValid pins the exact case IsValid's doc comment says it
// exists for: {Line: 0, Column: 5} is not the zero Pos (IsZero is false,
// since Column is nonzero), yet it cannot address a real character (IsValid
// is false too, since Line 0 is below the 1-based minimum). IsZero alone
// cannot be used to guard rendering; the two predicates disagree here.
func TestPos_ZeroButNotValid(t *testing.T) {
	p := Pos{Line: 0, Column: 5}

	if p.IsZero() {
		t.Fatal("IsZero() = true, want false: Column is non-zero")
	}
	if p.IsValid() {
		t.Fatal("IsValid() = true, want false: Line 0 cannot address a real line")
	}
}

func TestNewAt(t *testing.T) {
	start := Pos{Line: 1, Column: 5}
	end := Pos{Line: 1, Column: 15}

	at := NewAt("created_at", start, end)

	if at.Value != "created_at" {
		t.Errorf("Value = %q, want %q", at.Value, "created_at")
	}
	if at.Pos != start {
		t.Errorf("Pos = %+v, want %+v", at.Pos, start)
	}
	if at.End != end {
		t.Errorf("End = %+v, want %+v", at.End, end)
	}
}

func TestBare(t *testing.T) {
	at := Bare(42)

	if at.Value != 42 {
		t.Errorf("Value = %v, want 42", at.Value)
	}
	if !at.Pos.IsZero() {
		t.Errorf("Pos = %+v, want the zero Pos: Bare values were synthesised, not read from a schema", at.Pos)
	}
	if !at.End.IsZero() {
		t.Errorf("End = %+v, want the zero Pos", at.End)
	}
}

func TestFile(t *testing.T) {
	f := File{Name: "lapigo.yaml", Src: []byte("entities:\n")}

	if f.Name != "lapigo.yaml" {
		t.Errorf("Name = %q, want %q", f.Name, "lapigo.yaml")
	}
	if string(f.Src) != "entities:\n" {
		t.Errorf("Src = %q, want %q", string(f.Src), "entities:\n")
	}
}
