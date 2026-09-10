package gen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// GenDir is rewritten in full on every run. It is never edited by hand
	// (CLAUDE.md decision 2); Write's whole job is replacing it safely.
	GenDir = "internal/gen"

	// StagingDir is where a run's output is written before it replaces
	// GenDir. The leading dot matters: the Go toolchain ignores any
	// directory beginning with "." or "_" (spec 5.4), so a staging
	// directory left behind by a crash does not break `go build ./...` --
	// it is simply invisible to the build. A plain sibling such as
	// "internal/gen-staging" would be compiled instead, producing a
	// duplicate "package model" (etc.) declaration alongside the real one.
	StagingDir = "internal/.lapigo-staging"

	// OldDir is what GenDir is renamed to while StagingDir is renamed into
	// its place (spec 5.4). It shares StagingDir's dot-prefix rationale:
	// the previous generation's content sits here only for the instant
	// between the two renames, but the Go toolchain must ignore it for
	// that instant too.
	OldDir = "internal/.lapigo-old"
)

// Options controls one Write call.
type Options struct {
	// Force overrides the stops in spec 5.5's rows 1, 4 and 7: a modified
	// generated file, a missing emitted-once file, and internal/gen present
	// with no lock to explain it. It does not override row 5 -- a file
	// inside internal/gen the lock has never seen -- because the spec is
	// explicit that lapigo "does not guess" there, and DecideEmittedOnce's
	// "present, no entry" state for the same reason: overwriting applied
	// migration history is not a stop a single flag should paper over.
	Force bool

	// Version is the generator version recorded in every "generated" entry
	// this call rewrites. It is supplied by the caller -- pinned at build
	// time via -ldflags -X per spec 5.3 -- rather than read from a package
	// global, so Write has no hidden dependency on how its caller's binary
	// was built and is trivial to exercise with a fixed value in a test.
	Version string
}

// ConflictError reports every file that must be resolved by hand, or with
// --force, before Write can proceed. Every issue found is collected before
// returning, rather than stopping at the first one, so a single run tells a
// user everything that needs attention.
type ConflictError struct {
	// Issues is one line per conflicting file, sorted for determinism.
	Issues []string
}

func (e *ConflictError) Error() string {
	return strings.Join(e.Issues, "\n")
}

// Write stages files -- keyed by a slash-separated path relative to
// internal/gen, e.g. "model/article.go" -- under internal/.lapigo-staging,
// swaps them into internal/gen, rewrites the "generated" entries of
// .lapigo.lock, and returns the updated lock. root is the generated
// project's root directory, the one containing go.mod and internal/.
//
// files must contain Go source only (spec 5.4): the initial migration is
// not part of this map, is never staged or swapped by this function, and is
// covered by an "emitted-once" lock entry that DecideEmittedOnce and the
// Lock methods above manage independently of Write -- deliberately, so a
// caller (step 9's CLI, which owns migrations/0001_init.sql) can drive that
// decision and then Set/Save the resulting entry without this package ever
// writing a non-Go file.
//
// Write runs Recover first, so a .lapigo-old or .lapigo-staging left by a
// process that died on a previous run does not block or corrupt this one.
//
// Two renames are not one atomic operation (spec 5.4, 12): if the process
// dies between renaming GenDir aside and renaming StagingDir into place,
// Write attempts an immediate best-effort rollback and, failing that,
// leaves the state for the next Recover call to repair -- internal/gen
// missing and internal/.lapigo-old holding the previous generation is
// exactly the state Recover exists to fix.
func Write(root string, files map[string][]byte, opts Options) (*Lock, error) {
	if opts.Version == "" {
		return nil, fmt.Errorf("gen: Options.Version must not be empty")
	}
	for key := range files {
		if err := validateOutputPath(key); err != nil {
			return nil, err
		}
	}
	if _, err := Recover(root); err != nil {
		return nil, err
	}

	lock, err := LoadLock(root)
	lockAbsent := errors.Is(err, ErrLockNotFound)
	if err != nil && !lockAbsent {
		return nil, err
	}
	if lockAbsent {
		lock = NewLock(opts.Version)
	}

	issues, err := checkGenerated(root, lock, lockAbsent, opts.Force)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, &ConflictError{Issues: issues}
	}

	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	stagingPath := filepath.Join(root, filepath.FromSlash(StagingDir))
	if err := stageFiles(stagingPath, keys, files); err != nil {
		return nil, err
	}

	if err := swap(root, stagingPath); err != nil {
		return nil, err
	}

	for _, e := range lock.EntriesByKind(KindGenerated) {
		lock.Remove(e.Path)
	}
	for _, key := range keys {
		lock.Set(Entry{
			Path:   path.Join(GenDir, key),
			Kind:   KindGenerated,
			SHA256: Checksum(files[key]),
		})
	}
	lock.Version = opts.Version

	if err := lock.Save(root); err != nil {
		return nil, fmt.Errorf("gen: %s was written but %s: %w", GenDir, LockFileName, err)
	}
	return lock, nil
}

// stageFiles clears and repopulates stagingPath with files, keyed by a path
// relative to stagingPath itself. keys is passed in (rather than ranged
// from files) so the caller controls iteration order; it does not change
// what ends up on disk; it only makes directory-creation order deterministic
// for a reader of a trace, since the file *contents* are byte-identical
// regardless of write order.
func stageFiles(stagingPath string, keys []string, files map[string][]byte) error {
	if err := os.RemoveAll(stagingPath); err != nil {
		return fmt.Errorf("gen: clear %s: %w", StagingDir, err)
	}
	if err := os.MkdirAll(stagingPath, 0o755); err != nil {
		return fmt.Errorf("gen: create %s: %w", StagingDir, err)
	}
	for _, key := range keys {
		dest := filepath.Join(stagingPath, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			os.RemoveAll(stagingPath)
			return fmt.Errorf("gen: create directory for %s: %w", key, err)
		}
		if err := os.WriteFile(dest, files[key], 0o644); err != nil {
			os.RemoveAll(stagingPath)
			return fmt.Errorf("gen: write %s: %w", key, err)
		}
	}
	return nil
}

// swap performs spec 5.4 step 3: rename GenDir aside, rename stagingPath
// into GenDir's place, remove the old one. See Write's doc comment for the
// recovery behaviour when the second rename fails.
func swap(root, stagingPath string) error {
	genPath := filepath.Join(root, filepath.FromSlash(GenDir))
	oldPath := filepath.Join(root, filepath.FromSlash(OldDir))

	if err := os.MkdirAll(filepath.Dir(genPath), 0o755); err != nil {
		os.RemoveAll(stagingPath)
		return fmt.Errorf("gen: create %s: %w", filepath.Dir(GenDir), err)
	}

	genExisted := dirExists(genPath)
	if genExisted {
		if err := os.Rename(genPath, oldPath); err != nil {
			os.RemoveAll(stagingPath)
			return fmt.Errorf("gen: rename %s aside to %s: %w", GenDir, OldDir, err)
		}
	}

	if err := os.Rename(stagingPath, genPath); err != nil {
		if !genExisted {
			return fmt.Errorf("gen: swap %s into place: %w", StagingDir, err)
		}
		if rbErr := os.Rename(oldPath, genPath); rbErr == nil {
			return fmt.Errorf("gen: swap %s into place: %w (rolled back to the previous %s)", StagingDir, err, GenDir)
		}
		return fmt.Errorf("gen: swap %s into place: %w; automatic rollback also failed: %s is now MISSING and the previous contents are in %s -- re-run to recover, or rename %s to %s by hand", StagingDir, err, GenDir, OldDir, OldDir, GenDir)
	}

	if genExisted {
		// Best-effort: a leftover .lapigo-old is cosmetic once the swap
		// above has succeeded, and Recover cleans up exactly this leftover
		// on the next run, so a failure here does not fail Write.
		_ = os.RemoveAll(oldPath)
	}
	return nil
}

// checkGenerated implements spec 5.5's decision table for the "generated"
// kind: rows 1 (modified file), 5 (unmanaged file) and 7 (lock absent,
// internal/gen present). Rows 3 and 6 need no issue reported -- they are
// spec 5.5's "proceed" rows -- so absence from the returned slice already
// covers them.
//
// force filters out rows 1 and 7; row 5 is never filtered, matching spec
// 5.5's "the tool does not guess" -- no flag deletes a file lapigo cannot
// explain.
func checkGenerated(root string, lock *Lock, lockAbsent bool, force bool) ([]string, error) {
	genPath := filepath.Join(root, filepath.FromSlash(GenDir))
	genExists := dirExists(genPath)

	if lockAbsent {
		if genExists && !force {
			return []string{fmt.Sprintf("%s: exists but %s is missing; lapigo cannot tell whether its contents were hand-edited (re-run with --force to overwrite it, or remove %s yourself first)", GenDir, LockFileName, GenDir)}, nil
		}
		return nil, nil
	}

	var issues []string
	known := map[string]bool{}
	for _, e := range lock.EntriesByKind(KindGenerated) {
		known[e.Path] = true
		abs := filepath.Join(root, filepath.FromSlash(e.Path))
		content, err := os.ReadFile(abs)
		if errors.Is(err, os.ErrNotExist) {
			continue // row 3: file absent, entry present -> proceed.
		}
		if err != nil {
			return nil, fmt.Errorf("gen: read %s: %w", e.Path, err)
		}
		if Checksum(content) != e.SHA256 && !force {
			issues = append(issues, fmt.Sprintf("%s: modified after lapigo generated it; refusing to overwrite (re-run with --force to overwrite)", e.Path))
		}
	}

	if genExists {
		err := filepath.WalkDir(genPath, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return relErr
			}
			relSlash := filepath.ToSlash(rel)
			if !known[relSlash] {
				issues = append(issues, fmt.Sprintf("%s: present in %s but not recorded in %s; lapigo does not know whether it is hand-added or stale, and will not delete it (remove it by hand, or move it out of %s)", relSlash, GenDir, LockFileName, GenDir))
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("gen: scan %s: %w", GenDir, err)
		}
	}

	sort.Strings(issues)
	return issues, nil
}

// validateOutputPath rejects any files key that is not a clean,
// slash-separated path relative to internal/gen. Generate is trusted
// (nothing here defends against an adversarial schema), but a template bug
// producing "../x" or an absolute path must fail loudly rather than write
// outside internal/gen.
func validateOutputPath(key string) error {
	if key == "" {
		return fmt.Errorf("gen: empty output path")
	}
	if strings.Contains(key, "\\") {
		return fmt.Errorf("gen: output path %q contains a backslash; paths must use \"/\"", key)
	}
	if path.IsAbs(key) {
		return fmt.Errorf("gen: output path %q must be relative", key)
	}
	clean := path.Clean(key)
	if clean != key {
		return fmt.Errorf("gen: output path %q is not in clean form (expected %q)", key, clean)
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("gen: output path %q escapes internal/gen", key)
	}
	return nil
}

// RecoverResult describes what Recover found and repaired.
type RecoverResult struct {
	// RestoredGenDir is true if internal/gen was missing and
	// internal/.lapigo-old was renamed back into its place.
	RestoredGenDir bool
	// RemovedOldDir is true if both internal/gen and internal/.lapigo-old
	// were present -- a crash after the swap completed but before the old
	// directory's removal -- and the stale internal/.lapigo-old was
	// deleted.
	RemovedOldDir bool
	// RemovedStaging is true if an orphaned internal/.lapigo-staging (left
	// by a crash before the swap began) was deleted.
	RemovedStaging bool
}

// Recover repairs the on-disk state a process can leave behind when it is
// interrupted between spec 5.4's two renames, or while writing staging.
// Write calls it first, so a leftover .lapigo-old or .lapigo-staging from
// an earlier interrupted run never blocks or corrupts a later one; a
// caller (step 9's CLI) may also call it directly at startup to report what
// it found before running Generate.
//
// Two renames are not one atomic operation (spec 5.4, 12): this is the
// explicit recovery path the spec requires instead of a false atomicity
// claim.
func Recover(root string) (RecoverResult, error) {
	var res RecoverResult

	genPath := filepath.Join(root, filepath.FromSlash(GenDir))
	oldPath := filepath.Join(root, filepath.FromSlash(OldDir))
	stagingPath := filepath.Join(root, filepath.FromSlash(StagingDir))

	genExists := dirExists(genPath)
	oldExists := dirExists(oldPath)

	switch {
	case !genExists && oldExists:
		// The crash landed between the two renames: internal/gen is
		// missing and the previous generation is sitting in
		// internal/.lapigo-old. Restore it.
		if err := os.Rename(oldPath, genPath); err != nil {
			return res, fmt.Errorf("gen: recover %s from %s: %w", GenDir, OldDir, err)
		}
		res.RestoredGenDir = true
	case genExists && oldExists:
		// The crash landed after the swap completed but before the old
		// directory's removal: internal/gen already holds the new
		// generation, and internal/.lapigo-old is a stale leftover.
		if err := os.RemoveAll(oldPath); err != nil {
			return res, fmt.Errorf("gen: remove stale %s: %w", OldDir, err)
		}
		res.RemovedOldDir = true
	}

	if dirExists(stagingPath) {
		if err := os.RemoveAll(stagingPath); err != nil {
			return res, fmt.Errorf("gen: sweep orphaned %s: %w", StagingDir, err)
		}
		res.RemovedStaging = true
	}

	return res, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
