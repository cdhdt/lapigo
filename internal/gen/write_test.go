package gen

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(b)
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestWrite_FirstRun(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"model/article.go": []byte("package model\n"),
		"store/article.go": []byte("package store\n"),
	}

	lock, err := Write(root, files, Options{Version: "v1"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := readFileString(t, filepath.Join(root, "internal/gen/model/article.go"))
	if got != "package model\n" {
		t.Fatalf("model/article.go = %q, want %q", got, "package model\n")
	}
	got = readFileString(t, filepath.Join(root, "internal/gen/store/article.go"))
	if got != "package store\n" {
		t.Fatalf("store/article.go = %q, want %q", got, "package store\n")
	}

	if lock.Version != "v1" {
		t.Fatalf("lock.Version = %q, want v1", lock.Version)
	}
	e, ok := lock.Entry("internal/gen/model/article.go")
	if !ok {
		t.Fatalf("lock has no entry for internal/gen/model/article.go")
	}
	if e.Kind != KindGenerated {
		t.Fatalf("entry kind = %v, want %v", e.Kind, KindGenerated)
	}
	if e.SHA256 != Checksum([]byte("package model\n")) {
		t.Fatalf("entry checksum = %q, want %q", e.SHA256, Checksum([]byte("package model\n")))
	}

	// Persisted lock file must reflect the same two entries.
	reloaded, err := LoadLock(root)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(reloaded.EntriesByKind(KindGenerated)) != 2 {
		t.Fatalf("reloaded lock has %d generated entries, want 2", len(reloaded.EntriesByKind(KindGenerated)))
	}

	// internal/.lapigo-staging and internal/.lapigo-old must not survive a
	// clean run.
	if dirExists(filepath.Join(root, StagingDir)) {
		t.Fatalf("%s left behind after a clean run", StagingDir)
	}
	if dirExists(filepath.Join(root, OldDir)) {
		t.Fatalf("%s left behind after a clean run", OldDir)
	}
}

func TestWrite_RegenerateDropsRemovedFiles(t *testing.T) {
	root := t.TempDir()
	first := map[string][]byte{
		"model/article.go": []byte("package model\n// v1\n"),
		"model/author.go":  []byte("package model\n// author\n"),
	}
	if _, err := Write(root, first, Options{Version: "v1"}); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	second := map[string][]byte{
		"model/article.go": []byte("package model\n// v2\n"),
	}
	lock, err := Write(root, second, Options{Version: "v2"})
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}

	if dirExists(filepath.Join(root, "internal/gen/model/author.go")) {
		t.Fatalf("author.go should have been dropped on regeneration")
	}
	got := readFileString(t, filepath.Join(root, "internal/gen/model/article.go"))
	if got != "package model\n// v2\n" {
		t.Fatalf("article.go = %q, want v2 content", got)
	}
	if _, ok := lock.Entry("internal/gen/model/author.go"); ok {
		t.Fatalf("lock still has a stale entry for author.go")
	}
	if lock.Version != "v2" {
		t.Fatalf("lock.Version = %q, want v2", lock.Version)
	}
}

// TestWrite_RefusesHandEditedFile is a guard test: build the exact state a
// hand edit produces (lock recorded the old checksum, disk holds something
// else), run Write, and watch it refuse rather than silently destroying the
// edit.
func TestWrite_RefusesHandEditedFile(t *testing.T) {
	root := t.TempDir()
	orig := map[string][]byte{"model/article.go": []byte("package model\n// v1\n")}
	if _, err := Write(root, orig, Options{Version: "v1"}); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	handEdited := "package model\n// hand-edited by a human\n"
	mustWriteFile(t, filepath.Join(root, "internal/gen/model/article.go"), handEdited)

	next := map[string][]byte{"model/article.go": []byte("package model\n// v2\n")}
	_, err := Write(root, next, Options{Version: "v2"})
	if err == nil {
		t.Fatalf("Write succeeded on a hand-edited file; the edit would have been destroyed")
	}
	want := "internal/gen/model/article.go: modified after lapigo generated it; refusing to overwrite (re-run with --force to overwrite)"
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}

	// The refusal must not have touched the file or the lock.
	got := readFileString(t, filepath.Join(root, "internal/gen/model/article.go"))
	if got != handEdited {
		t.Fatalf("hand-edited file was modified despite the refusal: %q", got)
	}
	lock, err := LoadLock(root)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if lock.Version != "v1" {
		t.Fatalf("lock.Version = %q after a refused Write, want v1 (unchanged)", lock.Version)
	}
}

// TestWrite_ForceOverwritesHandEditedFile watches the same guard yield when
// --force is passed, per section 5.5 row 1.
func TestWrite_ForceOverwritesHandEditedFile(t *testing.T) {
	root := t.TempDir()
	orig := map[string][]byte{"model/article.go": []byte("package model\n// v1\n")}
	if _, err := Write(root, orig, Options{Version: "v1"}); err != nil {
		t.Fatalf("seed Write: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "internal/gen/model/article.go"), "hand edit\n")

	next := map[string][]byte{"model/article.go": []byte("package model\n// v2\n")}
	lock, err := Write(root, next, Options{Version: "v2", Force: true})
	if err != nil {
		t.Fatalf("forced Write: %v", err)
	}
	got := readFileString(t, filepath.Join(root, "internal/gen/model/article.go"))
	if got != "package model\n// v2\n" {
		t.Fatalf("article.go = %q, want v2 content after --force", got)
	}
	if lock.Version != "v2" {
		t.Fatalf("lock.Version = %q, want v2", lock.Version)
	}
}

// TestWrite_RefusesUnmanagedFile_EvenWithForce is a guard test for section
// 5.5's row without a --force override: a file inside internal/gen that the
// lock never recorded must stop generation, and --force must not make it
// disappear silently.
func TestWrite_RefusesUnmanagedFile_EvenWithForce(t *testing.T) {
	root := t.TempDir()
	orig := map[string][]byte{"model/article.go": []byte("package model\n")}
	if _, err := Write(root, orig, Options{Version: "v1"}); err != nil {
		t.Fatalf("seed Write: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "internal/gen/model/extra.go"), "package model\n// hand-added\n")

	next := map[string][]byte{"model/article.go": []byte("package model\n// v2\n")}

	for _, force := range []bool{false, true} {
		_, err := Write(root, next, Options{Version: "v2", Force: force})
		if err == nil {
			t.Fatalf("Write(force=%v) succeeded with an unmanaged file present", force)
		}
		want := "internal/gen/model/extra.go: present in internal/gen but not recorded in .lapigo.lock; lapigo does not know whether it is hand-added or stale, and will not delete it (remove it by hand, or move it out of internal/gen)"
		if err.Error() != want {
			t.Fatalf("force=%v: err = %q, want %q", force, err.Error(), want)
		}
	}

	// The unmanaged file itself must have survived every attempt.
	got := readFileString(t, filepath.Join(root, "internal/gen/model/extra.go"))
	if got != "package model\n// hand-added\n" {
		t.Fatalf("unmanaged file was altered: %q", got)
	}
}

// TestWrite_ReportsMultipleConflictsTogether pins ConflictError's aggregate
// shape: a modified file and an unmanaged file present at once must both be
// named in one error, sorted by path, joined by newline -- not just the
// first one found.
func TestWrite_ReportsMultipleConflictsTogether(t *testing.T) {
	root := t.TempDir()
	orig := map[string][]byte{
		"model/article.go": []byte("package model\n// v1\n"),
		"model/author.go":  []byte("package model\n// author\n"),
	}
	if _, err := Write(root, orig, Options{Version: "v1"}); err != nil {
		t.Fatalf("seed Write: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "internal/gen/model/article.go"), "hand edit\n")
	mustWriteFile(t, filepath.Join(root, "internal/gen/model/zzz_extra.go"), "package model\n// hand-added\n")

	next := map[string][]byte{"model/article.go": []byte("package model\n// v2\n")}
	_, err := Write(root, next, Options{Version: "v2"})
	if err == nil {
		t.Fatalf("Write succeeded with two outstanding conflicts")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want a *ConflictError", err, err)
	}
	want := []string{
		"internal/gen/model/article.go: modified after lapigo generated it; refusing to overwrite (re-run with --force to overwrite)",
		"internal/gen/model/zzz_extra.go: present in internal/gen but not recorded in .lapigo.lock; lapigo does not know whether it is hand-added or stale, and will not delete it (remove it by hand, or move it out of internal/gen)",
	}
	if len(ce.Issues) != len(want) {
		t.Fatalf("Issues = %#v, want %#v", ce.Issues, want)
	}
	for i := range want {
		if ce.Issues[i] != want[i] {
			t.Fatalf("Issues[%d] = %q, want %q", i, ce.Issues[i], want[i])
		}
	}
	wantErr := want[0] + "\n" + want[1]
	if err.Error() != wantErr {
		t.Fatalf("err.Error() = %q, want %q", err.Error(), wantErr)
	}
}

// TestWrite_RefusesGenDirWithoutLock is a guard test for the "lock absent,
// internal/gen present" row: nothing is known about that content, so Write
// must stop rather than rename it aside.
func TestWrite_RefusesGenDirWithoutLock(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "internal/gen/model/mystery.go"), "package model\n// pre-existing, no lock\n")

	files := map[string][]byte{"model/article.go": []byte("package model\n")}
	_, err := Write(root, files, Options{Version: "v1"})
	if err == nil {
		t.Fatalf("Write succeeded with internal/gen present and no lock file")
	}
	want := "internal/gen: exists but .lapigo.lock is missing; lapigo cannot tell whether its contents were hand-edited (re-run with --force to overwrite it, or remove internal/gen yourself first)"
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}

	// --force must let this one through, per section 5.5.
	lock, err := Write(root, files, Options{Version: "v1", Force: true})
	if err != nil {
		t.Fatalf("forced Write: %v", err)
	}
	if dirExists(filepath.Join(root, "internal/gen/model/mystery.go")) {
		t.Fatalf("mystery.go should have been replaced by the forced regeneration")
	}
	if _, ok := lock.Entry("internal/gen/model/article.go"); !ok {
		t.Fatalf("forced Write did not record the new file")
	}
}

func TestWrite_InvalidOutputPath(t *testing.T) {
	root := t.TempDir()
	tests := map[string]string{
		"../escape.go": `gen: output path "../escape.go" escapes internal/gen`,
		"/abs.go":      `gen: output path "/abs.go" must be relative`,
		"a//b.go":      `gen: output path "a//b.go" is not in clean form (expected "a/b.go")`,
		"":             `gen: empty output path`,
		`a\b.go`:       `gen: output path "a\\b.go" contains a backslash; paths must use "/"`,
	}
	for key, want := range tests {
		_, err := Write(root, map[string][]byte{key: []byte("x")}, Options{Version: "v1"})
		if err == nil || err.Error() != want {
			t.Fatalf("key %q: err = %v, want %q", key, err, want)
		}
	}
}

func TestWrite_RequiresVersion(t *testing.T) {
	root := t.TempDir()
	_, err := Write(root, map[string][]byte{"a.go": []byte("x")}, Options{})
	want := "gen: Options.Version must not be empty"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// TestWrite_Deterministic runs Write against two independent roots with the
// same inputs and asserts every resulting file, including the lock, is
// byte-identical (spec section 5.3).
func TestWrite_Deterministic(t *testing.T) {
	files := map[string][]byte{
		"model/article.go": []byte("package model\n"),
		"model/author.go":  []byte("package model\n// author\n"),
		"store/article.go": []byte("package store\n"),
	}

	rootA := t.TempDir()
	rootB := t.TempDir()

	if _, err := Write(rootA, files, Options{Version: "v1"}); err != nil {
		t.Fatalf("Write rootA: %v", err)
	}
	if _, err := Write(rootB, files, Options{Version: "v1"}); err != nil {
		t.Fatalf("Write rootB: %v", err)
	}

	var relPaths []string
	for k := range files {
		relPaths = append(relPaths, "internal/gen/"+k)
	}
	relPaths = append(relPaths, LockFileName)
	sort.Strings(relPaths)

	for _, rel := range relPaths {
		a := readFileString(t, filepath.Join(rootA, rel))
		b := readFileString(t, filepath.Join(rootB, rel))
		if a != b {
			t.Fatalf("%s differs between runs:\nA: %q\nB: %q", rel, a, b)
		}
	}
}

func TestRecover_NoOp(t *testing.T) {
	root := t.TempDir()
	res, err := Recover(root)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if res.RestoredGenDir || res.RemovedOldDir || res.RemovedStaging {
		t.Fatalf("Recover on an empty root reported action: %+v", res)
	}
}

func TestRecover_RestoresGenDirFromOld(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, OldDir, "model/article.go"), "package model\n// recovered\n")

	res, err := Recover(root)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !res.RestoredGenDir {
		t.Fatalf("Recover did not report RestoredGenDir")
	}
	got := readFileString(t, filepath.Join(root, "internal/gen/model/article.go"))
	if got != "package model\n// recovered\n" {
		t.Fatalf("recovered content = %q", got)
	}
	if dirExists(filepath.Join(root, OldDir)) {
		t.Fatalf("%s still present after recovery", OldDir)
	}
}

func TestRecover_RemovesStaleOldWhenGenPresent(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "internal/gen/model/article.go"), "package model\n// current\n")
	mustWriteFile(t, filepath.Join(root, OldDir, "model/article.go"), "package model\n// stale\n")

	res, err := Recover(root)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !res.RemovedOldDir {
		t.Fatalf("Recover did not report RemovedOldDir")
	}
	if dirExists(filepath.Join(root, OldDir)) {
		t.Fatalf("%s still present after recovery", OldDir)
	}
	got := readFileString(t, filepath.Join(root, "internal/gen/model/article.go"))
	if got != "package model\n// current\n" {
		t.Fatalf("current gen dir was disturbed: %q", got)
	}
}

func TestRecover_SweepsOrphanedStaging(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, StagingDir, "model/article.go"), "package model\n// orphaned\n")

	res, err := Recover(root)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !res.RemovedStaging {
		t.Fatalf("Recover did not report RemovedStaging")
	}
	if dirExists(filepath.Join(root, StagingDir)) {
		t.Fatalf("%s still present after recovery", StagingDir)
	}
}
