// Package gen owns the write half of code generation: staging rendered Go
// files under internal/.lapigo-staging, swapping them into internal/gen,
// recovering from a process that died mid-swap, and maintaining
// .lapigo.lock (spec sections 5.4, 5.5).
//
// This package never renders anything. Render-to-memory (templates,
// text/template, formatting) is a separate concern that hands this package
// a map[string][]byte; Write and Recover are fully exercised with synthetic
// content and no schema, no IR, and no template at all.
package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LockFileName is the name of the committed manifest recording, per file
// lapigo has written, enough to answer two different questions (spec 5.5):
// "was this hand-edited?" for a generated file about to be overwritten, and
// "is this behind the schema?" for the migration, which is never
// overwritten. It lives at the project root, alongside go.mod.
const LockFileName = ".lapigo.lock"

// lockFormatVersion is the first line of every .lapigo.lock file. It
// versions the file's own text format, not the generator; a future
// incompatible change to this layout bumps it and LoadLock rejects an
// older or newer one it cannot parse, rather than misreading it.
const lockFormatVersion = "lapigo.lock 1"

// EntryKind distinguishes the two questions a lock entry can answer (spec
// 5.5). The kind decides both what the checksum was computed from and
// whether it is ever compared to the file on disk.
type EntryKind string

const (
	// KindGenerated marks a file under internal/gen. Its checksum is the
	// file as written, and it is compared against disk on every run: a
	// mismatch means a hand edit is about to be destroyed.
	KindGenerated EntryKind = "generated"

	// KindEmittedOnce marks migrations/0001_init.sql. Its checksum is what
	// lapigo emitted when the file was created, never the file on disk, so
	// a hand edit to the migration can never move it -- only a schema
	// change (re-emitting and re-hashing) can. This is what lets the
	// migration be provenance-tracked without ever being overwritten.
	KindEmittedOnce EntryKind = "emitted-once"
)

// Entry is one line of .lapigo.lock: a file lapigo has written, which
// question its checksum answers, and the checksum itself.
type Entry struct {
	// Path is slash-separated and relative to the project root, e.g.
	// "internal/gen/model/article.go" or "migrations/0001_init.sql".
	Path string
	Kind EntryKind
	// SHA256 is the lower-case hex digest, exactly as Checksum returns it.
	SHA256 string
}

// Lock is the parsed contents of .lapigo.lock: the generator version that
// last rewrote the "generated" entries, plus one Entry per file lapigo
// knows about.
type Lock struct {
	// Version is the generator version, pinned at build time (spec 5.3)
	// and supplied by the caller -- this package never computes it.
	Version string

	entries map[string]Entry
}

// NewLock returns an empty lock for the given generator version, the value
// used both for a first run (no .lapigo.lock on disk yet) and as the
// starting point Write mutates before saving.
func NewLock(version string) *Lock {
	return &Lock{Version: version, entries: map[string]Entry{}}
}

// Entry looks up the entry recorded for path, if any.
func (l *Lock) Entry(path string) (Entry, bool) {
	e, ok := l.entries[path]
	return e, ok
}

// Set records (or replaces) the entry for e.Path.
func (l *Lock) Set(e Entry) {
	if l.entries == nil {
		l.entries = map[string]Entry{}
	}
	l.entries[e.Path] = e
}

// Remove deletes the entry for path, if any. Removing an absent path is a
// no-op.
func (l *Lock) Remove(path string) {
	delete(l.entries, path)
}

// EntriesByKind returns every entry of the given kind, sorted by Path. The
// sort exists because Go map iteration order is random and nothing that
// reaches a file or a comparison may depend on it (spec 5.3).
func (l *Lock) EntriesByKind(kind EntryKind) []Entry {
	var out []Entry
	for _, e := range l.entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// All returns every entry, sorted by Path.
func (l *Lock) All() []Entry {
	out := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ErrLockNotFound is returned by LoadLock when root has no .lapigo.lock.
// Callers distinguish "no lock yet" (a legitimate first run) from any other
// read failure with errors.Is(err, ErrLockNotFound).
var ErrLockNotFound = errors.New("gen: " + LockFileName + " not found")

// LoadLock reads and parses root's .lapigo.lock.
func LoadLock(root string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(root, LockFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrLockNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("gen: read %s: %w", LockFileName, err)
	}
	return parseLock(data)
}

// Save writes l to root's .lapigo.lock, replacing any existing file. The
// write goes through a temporary file in the same directory followed by a
// single os.Rename, which -- unlike the internal/gen swap in write.go -- is
// genuinely atomic on the platforms Go supports: one rename, not two.
//
// Entries are written sorted by Path so the file is deterministic (spec 5.3)
// and diffable in review, independent of Set's insertion order.
func (l *Lock) Save(root string) error {
	data, err := l.marshal()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, LockFileName)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("gen: write %s: %w", LockFileName, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("gen: replace %s: %w", LockFileName, err)
	}
	return nil
}

// marshal renders l in the on-disk format:
//
//	lapigo.lock 1
//	version <generator version>
//	<kind> sha256:<hex> <path>
//	...
//
// one entry line per Entry, sorted by Path. The format is line-based rather
// than JSON so a reviewer sees a minimal, git-diffable line change per
// affected file instead of a reordered object.
func (l *Lock) marshal() ([]byte, error) {
	if strings.ContainsAny(l.Version, " \t\n") {
		return nil, fmt.Errorf("gen: generator version %q contains whitespace", l.Version)
	}
	var b strings.Builder
	b.WriteString(lockFormatVersion)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "version %s\n", l.Version)
	for _, e := range l.All() {
		if e.Kind != KindGenerated && e.Kind != KindEmittedOnce {
			return nil, fmt.Errorf("gen: entry %q has unknown kind %q", e.Path, e.Kind)
		}
		if !isSHA256Hex(e.SHA256) {
			return nil, fmt.Errorf("gen: entry %q has malformed checksum %q", e.Path, e.SHA256)
		}
		if strings.ContainsAny(e.Path, " \t\n") {
			return nil, fmt.Errorf("gen: entry path %q contains whitespace", e.Path)
		}
		fmt.Fprintf(&b, "%s sha256:%s %s\n", e.Kind, e.SHA256, e.Path)
	}
	return []byte(b.String()), nil
}

// parseLock is marshal's inverse. Every rejection names the exact line: the
// error text is part of this package's contract with whoever reads a
// corrupted lock file.
func parseLock(data []byte) (*Lock, error) {
	text := string(data)
	text = strings.TrimSuffix(text, "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}

	header := ""
	if len(lines) > 0 {
		header = lines[0]
	}
	if header != lockFormatVersion {
		return nil, fmt.Errorf("gen: %s: unrecognized header %q, want %q", LockFileName, header, lockFormatVersion)
	}
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "version ") {
		return nil, fmt.Errorf("gen: %s: line 2: expected %q", LockFileName, "version <string>")
	}

	l := NewLock(strings.TrimPrefix(lines[1], "version "))
	for i, line := range lines[2:] {
		lineNo := i + 3
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("gen: %s: line %d: want 3 fields, got %d: %q", LockFileName, lineNo, len(fields), line)
		}
		kind := EntryKind(fields[0])
		if kind != KindGenerated && kind != KindEmittedOnce {
			return nil, fmt.Errorf("gen: %s: line %d: unknown entry kind %q", LockFileName, lineNo, fields[0])
		}
		sum, ok := strings.CutPrefix(fields[1], "sha256:")
		if !ok || !isSHA256Hex(sum) {
			return nil, fmt.Errorf("gen: %s: line %d: malformed checksum %q", LockFileName, lineNo, fields[1])
		}
		path := fields[2]
		if _, dup := l.entries[path]; dup {
			return nil, fmt.Errorf("gen: %s: line %d: duplicate entry for %q", LockFileName, lineNo, path)
		}
		l.Set(Entry{Path: path, Kind: kind, SHA256: sum})
	}
	return l, nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// Checksum returns the lower-case hex SHA-256 digest of content. Every lock
// entry's SHA256 field is produced by this function, on both sides of every
// comparison the decision table makes.
func Checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// EmittedOnceAction is what DecideEmittedOnce says to do about an
// emitted-once entry (spec 5.5, 6.1).
type EmittedOnceAction int

const (
	// EmittedOnceWrite means: create the file with today's emission, and
	// record its checksum as the new emitted checksum.
	EmittedOnceWrite EmittedOnceAction = iota
	// EmittedOnceKeep means: do nothing. The schema has not moved since
	// the file was created.
	EmittedOnceKeep
	// EmittedOnceWarn means: the schema has moved since the file was
	// created. Tell the user, but never touch the file.
	EmittedOnceWarn
)

// String renders a out-of-range value as an obviously bogus string rather
// than a plausible neighbour, so a test asserting on it can tell the
// difference between "warn" and "an enum gone wrong".
func (a EmittedOnceAction) String() string {
	switch a {
	case EmittedOnceWrite:
		return "write"
	case EmittedOnceKeep:
		return "keep"
	case EmittedOnceWarn:
		return "warn"
	default:
		return fmt.Sprintf("EmittedOnceAction(%d)", int(a))
	}
}

// DecideEmittedOnce implements the decision for one emitted-once entry
// (spec 5.5's table, restated in full for the migration by section 6.1),
// without ever touching a file itself.
//
// It never hashes fileExists's target: an emitted-once checksum records
// what lapigo emitted at creation, not what is on disk, precisely so a hand
// edit cannot move it. emittedChecksum is Checksum of whatever the caller
// would emit today (e.g. sha256(ddl.Emit(schema))); computing that is the
// caller's job; this package has no notion of what an emitted-once file
// contains.
//
// The states:
//
//	absent, no entry            -> Write (first creation)
//	present, checksums equal    -> Keep (schema has not moved)
//	present, checksums differ   -> Warn (schema moved; never touch the file)
//	absent, entry present       -> error; --force re-emits it (row 4)
//	present, entry absent       -> error, never forced: this is applied
//	                                migration history the tool will not
//	                                silently adopt or overwrite
func DecideEmittedOnce(lock *Lock, path, emittedChecksum string, fileExists, force bool) (EmittedOnceAction, error) {
	entry, present := lock.Entry(path)
	switch {
	case !present && !fileExists:
		return EmittedOnceWrite, nil
	case !present && fileExists:
		return 0, fmt.Errorf("%s: present on disk but not recorded in %s; lapigo does not know whether it is hand-added or a leftover from before %s existed, and will not overwrite applied migration history (move it aside, or add its checksum to %s by hand)", path, LockFileName, LockFileName, LockFileName)
	case present && !fileExists:
		if force {
			return EmittedOnceWrite, nil
		}
		return 0, fmt.Errorf("%s: recorded in %s but missing on disk; restore it from version control, or re-run with --force to re-emit it", path, LockFileName)
	default: // present && fileExists
		if entry.SHA256 == emittedChecksum {
			return EmittedOnceKeep, nil
		}
		return EmittedOnceWarn, nil
	}
}
