package gen

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestChecksum pins the exact hex digest for a known input. sha256("hello
// world") is a fixed, independently verifiable value (computed here via
// python3/sha256sum, not via crypto/sha256 itself) -- comparing against a
// literal catches a wrong encoding (upper-case, base64, byte-reversed) or a
// swapped hash function, not just "did we call some hash function".
func TestChecksum(t *testing.T) {
	got := Checksum([]byte("hello world"))
	want := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if got != want {
		t.Fatalf("Checksum(%q) = %q, want %q", "hello world", got, want)
	}
}

func TestChecksum_EmptyInput(t *testing.T) {
	got := Checksum(nil)
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Fatalf("Checksum(nil) = %q, want %q", got, want)
	}
}

// TestLock_SaveLoadRoundTrip exercises Save/LoadLock together: the file
// written by Save must be exactly what LoadLock parses back into the same
// entries, sorted by path regardless of insertion order.
func TestLock_SaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	l := NewLock("abc123")
	l.Set(Entry{Path: "internal/gen/store/z.go", Kind: KindGenerated, SHA256: sha32("z")})
	l.Set(Entry{Path: "internal/gen/model/a.go", Kind: KindGenerated, SHA256: sha32("a")})
	l.Set(Entry{Path: "migrations/0001_init.sql", Kind: KindEmittedOnce, SHA256: sha32("m")})

	if err := l.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, LockFileName))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "lapigo.lock 1\n" +
		"version abc123\n" +
		"generated sha256:" + sha32("a") + " internal/gen/model/a.go\n" +
		"generated sha256:" + sha32("z") + " internal/gen/store/z.go\n" +
		"emitted-once sha256:" + sha32("m") + " migrations/0001_init.sql\n"
	if string(data) != want {
		t.Fatalf("saved lock file =\n%s\nwant\n%s", data, want)
	}

	loaded, err := LoadLock(root)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if loaded.Version != "abc123" {
		t.Fatalf("loaded.Version = %q, want %q", loaded.Version, "abc123")
	}
	e, ok := loaded.Entry("internal/gen/model/a.go")
	if !ok || e != (Entry{Path: "internal/gen/model/a.go", Kind: KindGenerated, SHA256: sha32("a")}) {
		t.Fatalf("loaded entry for a.go = %+v, ok=%v", e, ok)
	}
	e, ok = loaded.Entry("migrations/0001_init.sql")
	if !ok || e != (Entry{Path: "migrations/0001_init.sql", Kind: KindEmittedOnce, SHA256: sha32("m")}) {
		t.Fatalf("loaded entry for migration = %+v, ok=%v", e, ok)
	}
}

func TestLoadLock_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := LoadLock(root)
	if !errors.Is(err, ErrLockNotFound) {
		t.Fatalf("LoadLock on empty dir: err = %v, want ErrLockNotFound", err)
	}
}

func TestParseLock_BadHeader(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, LockFileName), "not-lapigo-lock\n")
	_, err := LoadLock(root)
	want := `gen: .lapigo.lock: unrecognized header "not-lapigo-lock", want "lapigo.lock 1"`
	if err == nil || err.Error() != want {
		t.Fatalf("LoadLock error = %v, want %q", err, want)
	}
}

func TestParseLock_MissingVersionLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, LockFileName), "lapigo.lock 1\n")
	_, err := LoadLock(root)
	want := `gen: .lapigo.lock: line 2: expected "version <string>"`
	if err == nil || err.Error() != want {
		t.Fatalf("LoadLock error = %v, want %q", err, want)
	}
}

func TestParseLock_BadEntryLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, LockFileName), "lapigo.lock 1\nversion v1\ngenerated onlytwo\n")
	_, err := LoadLock(root)
	want := `gen: .lapigo.lock: line 3: want 3 fields, got 2: "generated onlytwo"`
	if err == nil || err.Error() != want {
		t.Fatalf("LoadLock error = %v, want %q", err, want)
	}
}

func TestParseLock_UnknownKind(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, LockFileName), "lapigo.lock 1\nversion v1\nbogus sha256:"+sha32("x")+" internal/gen/a.go\n")
	_, err := LoadLock(root)
	want := `gen: .lapigo.lock: line 3: unknown entry kind "bogus"`
	if err == nil || err.Error() != want {
		t.Fatalf("LoadLock error = %v, want %q", err, want)
	}
}

func TestParseLock_MalformedChecksum(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, LockFileName), "lapigo.lock 1\nversion v1\ngenerated sha256:notahexstring internal/gen/a.go\n")
	_, err := LoadLock(root)
	want := `gen: .lapigo.lock: line 3: malformed checksum "sha256:notahexstring"`
	if err == nil || err.Error() != want {
		t.Fatalf("LoadLock error = %v, want %q", err, want)
	}
}

func TestParseLock_DuplicateEntry(t *testing.T) {
	root := t.TempDir()
	line := "generated sha256:" + sha32("a") + " internal/gen/a.go\n"
	writeFile(t, filepath.Join(root, LockFileName), "lapigo.lock 1\nversion v1\n"+line+line)
	_, err := LoadLock(root)
	want := `gen: .lapigo.lock: line 4: duplicate entry for "internal/gen/a.go"`
	if err == nil || err.Error() != want {
		t.Fatalf("LoadLock error = %v, want %q", err, want)
	}
}

// TestDecideEmittedOnce_Table pins every state of section 5.5 / 6.1's
// decision table for the emitted-once kind, plus --force for each state that
// spec text actually grants it to.
func TestDecideEmittedOnce_Table(t *testing.T) {
	const path = "migrations/0001_init.sql"
	sumA := sha32("emit-a")
	sumB := sha32("emit-b")

	tests := []struct {
		name        string
		lock        *Lock
		emitted     string
		fileExists  bool
		force       bool
		wantAction  EmittedOnceAction
		wantErrText string
	}{
		{
			name:       "absent, no entry: write",
			lock:       NewLock("v1"),
			emitted:    sumA,
			fileExists: false,
			wantAction: EmittedOnceWrite,
		},
		{
			name:       "present, checksum matches recorded emission: keep",
			lock:       lockWith(path, KindEmittedOnce, sumA),
			emitted:    sumA,
			fileExists: true,
			wantAction: EmittedOnceKeep,
		},
		{
			name:       "present, emitted checksum differs: warn, never stop",
			lock:       lockWith(path, KindEmittedOnce, sumA),
			emitted:    sumB,
			fileExists: true,
			wantAction: EmittedOnceWarn,
		},
		{
			name:        "absent, entry present: stop",
			lock:        lockWith(path, KindEmittedOnce, sumA),
			emitted:     sumA,
			fileExists:  false,
			wantErrText: `migrations/0001_init.sql: recorded in .lapigo.lock but missing on disk; restore it from version control, or re-run with --force to re-emit it`,
		},
		{
			name:       "absent, entry present, --force: write",
			lock:       lockWith(path, KindEmittedOnce, sumA),
			emitted:    sumA,
			fileExists: false,
			force:      true,
			wantAction: EmittedOnceWrite,
		},
		{
			name:        "present, no entry: stop, never forced",
			lock:        NewLock("v1"),
			emitted:     sumA,
			fileExists:  true,
			force:       true,
			wantErrText: `migrations/0001_init.sql: present on disk but not recorded in .lapigo.lock; lapigo does not know whether it is hand-added or a leftover from before .lapigo.lock existed, and will not overwrite applied migration history (move it aside, or add its checksum to .lapigo.lock by hand)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, err := DecideEmittedOnce(tc.lock, path, tc.emitted, tc.fileExists, tc.force)
			if tc.wantErrText != "" {
				if err == nil || err.Error() != tc.wantErrText {
					t.Fatalf("err = %v, want %q", err, tc.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if action != tc.wantAction {
				t.Fatalf("action = %v, want %v", action, tc.wantAction)
			}
		})
	}
}

func lockWith(path string, kind EntryKind, sum string) *Lock {
	l := NewLock("v1")
	l.Set(Entry{Path: path, Kind: kind, SHA256: sum})
	return l
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// sha32 is a test helper: a syntactically valid 64-hex-char checksum derived
// from label, distinct labels producing distinct checksums, without pinning
// on Checksum's own behaviour (it does not call Checksum).
func sha32(label string) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 64)
	h := uint32(2166136261)
	for i := 0; i < len(b); i++ {
		h = (h ^ uint32(label[i%len(label)])) * 16777619
		b[i] = hex[int(h>>uint(4*(i%8)))&0xf]
	}
	return string(b)
}
