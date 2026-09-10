// Package gen renders a validated, frozen *ir.Schema into the Go source
// files of a generated project's internal/gen tree.
//
// It is steps 6 to 8 of the pipeline in spec §2.1 -- plan, render, format --
// and nothing else. Generate touches no filesystem, starts no subprocess and
// reads no environment: it takes an IR in and returns bytes out. Writing
// those bytes, comparing them against .lapigo.lock and swapping the staging
// directory into place are step 9's, above and below this package.
//
// That boundary is not a stylistic preference. It is what makes the golden,
// determinism and type-checking tests fast and race-free, it guarantees that
// a template failure leaves the user's tree untouched, and -- since the
// formatter runs with imports.FormatOnly (see format.go) -- it is what makes
// the same schema produce the same bytes on every machine, whatever happens
// to be in that machine's module cache.
//
// Templates consume the IR and only the IR (spec §2.2): never YAML, never a
// parser AST, never a map[string]any. Type mapping, casing, naming and the
// import set live in Go, in plan.go and in this file's funcMap, where they
// are unit-testable; the templates interpolate what those produce and decide
// almost nothing (spec §5.1).
package gen

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/cdhdt/lapigo/internal/ir"
)

// templatesFS holds every template this package renders. They are embedded
// rather than read from disk so that Generate keeps its no-filesystem
// contract, and so that a lapigo binary carries its own templates -- a
// generator whose output depended on files next to the executable would be
// a different generator on every install.
//
// There are no user-overridable templates and there is no lookup path
// (CLAUDE.md): making templates a public extension point would turn every
// release into a breaking change.
//
//go:embed templates
var templatesFS embed.FS

// templateGlob is the pattern ParseFS is given. The directory is flat and
// every file is named <package>_<subject>.tmpl, because ParseFS registers a
// template under its base name: two files called entity.tmpl in different
// subdirectories would silently clobber each other. Steps 6 to 8 add files
// here and change none of the existing ones.
const templateGlob = "templates/*.tmpl"

// Generate renders s into module-root-relative file paths and their
// formatted Go source (spec §2.1).
//
// modulePath is the target project's own module path -- the first line of
// its go.mod, e.g. "myapp" for a project created with `lapigo new myapp`
// (spec §6.1). It is needed starting with step 6, whose hooks package is the
// first one to import another generated package; see plan's doc comment for
// why Generate takes it as a parameter instead of resolving it some other
// way.
//
// Every returned path is under internal/gen and every returned value is a
// complete, gofmt-clean Go file. The initial migration is deliberately not
// in this map (spec §5.4): it is not Go, it must never reach the formatter,
// and it does not live under internal/gen. Keeping the map homogeneous is
// what lets step 9 stage, swap and checksum every entry with one code path.
//
// A failure anywhere aborts the whole run and returns a nil map. Partial
// output is never returned, because a caller that wrote it would leave the
// user with a tree that does not compile and a lock file that disagrees with
// it.
func Generate(s *ir.Schema, modulePath string) (map[string][]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("gen: Generate called on a nil schema")
	}
	if modulePath == "" {
		return nil, fmt.Errorf("gen: Generate called with an empty modulePath")
	}

	tmpl, err := newTemplate(templatesFS, templateGlob)
	if err != nil {
		return nil, err
	}

	files, err := plan(s, modulePath)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]byte, len(files))
	for _, f := range files {
		if _, dup := out[f.Path]; dup {
			// A map would silently keep the last writer. Two templates
			// claiming one path is a generator bug that must not reach a
			// user's disk as "the second one won".
			return nil, fmt.Errorf("gen: two output files claim the path %q", f.Path)
		}
		rendered, err := render(tmpl, f)
		if err != nil {
			return nil, err
		}
		formatted, err := format(f.Path, rendered)
		if err != nil {
			return nil, err
		}
		out[f.Path] = formatted
	}
	return out, nil
}

// newTemplate parses the templates matching patterns out of fsys.
//
// Option("missingkey=error") is mandatory (spec §5.1) and is set here, on
// the one constructor both Generate and the tests use, so that it cannot be
// set in one place and forgotten in another. Without it a mistyped key on a
// map renders the string "<no value>" into generated Go and the run
// succeeds: a silent wrong answer, which is the failure mode this project
// least tolerates.
func newTemplate(fsys fs.FS, patterns ...string) (*template.Template, error) {
	t, err := template.New("lapigo").
		Option("missingkey=error").
		Funcs(funcMap).
		ParseFS(fsys, patterns...)
	if err != nil {
		return nil, fmt.Errorf("gen: parse templates: %w", err)
	}
	return t, nil
}

// render executes f's template against f's data. The result is unformatted:
// format is a separate step so that a parse failure can present the text the
// template actually produced (spec §5.2).
func render(t *template.Template, f OutputFile) ([]byte, error) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, f.Template, f.Data); err != nil {
		return nil, fmt.Errorf("gen: render %s from template %q: %w", f.Path, f.Template, err)
	}
	return buf.Bytes(), nil
}

// funcMap is every function the templates may call. Each one exists because
// spec §5.1 puts the corresponding decision in Go rather than in template
// text: quoting, comment sanitisation, the shape of an import block, and the
// selection of an entity's enum fields.
var funcMap = template.FuncMap{
	"goString":           goString,
	"comment":            comment,
	"importBlock":        importBlock,
	"jsonTag":            jsonTag,
	"enumFields":         enumFields,
	"hookOperations":     hookOperations,
	"createInputMembers": createInputMembers,
	"updateInputMembers": updateInputMembers,
}

// goString renders s as a Go string literal, quotes included (spec §5.1).
// Every user string that reaches a Go literal goes through it; strconv.Quote
// escapes quotes, backslashes and every control character, so no schema
// value can terminate the literal it lands in.
func goString(s string) string { return strconv.Quote(s) }

// comment renders s for a Go comment position (spec §5.1).
//
// strconv.Quote does not cover comments: a value containing a newline ends
// the "//" and drops whatever followed it into the file as code, which is
// how a schema string turns into a syntax error -- or, worse, into
// something that parses. Every line terminator becomes a single space, and
// every other control character is dropped. The validator rejects control
// characters in names, so this is defence in depth and is meant to be: the
// two checks fail independently.
func comment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || r == '\v' || r == '\f':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Dropped, not spaced: a lone control character carries no
			// width a reader would miss.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// jsonTag renders the struct tag naming column as a field's JSON key.
//
// The key is Field.Column, one vocabulary for every place a field is named
// on the wire, and there is no omitempty anywhere in a model: a NULL column
// must serialise as null rather than vanish (spec §6.9.2).
//
// A tag is a raw string literal, so a backtick in the column would end it.
// The parser's identifier grammar cannot produce one, which is exactly why
// this returns an error instead of trusting that: if the grammar ever
// widens, generation stops rather than emitting a file whose tag has
// silently swallowed the rest of the struct.
func jsonTag(column string) (string, error) {
	if strings.ContainsRune(column, '`') {
		return "", fmt.Errorf("gen: column %q contains a backtick, which cannot appear in a struct tag", column)
	}
	if strings.ContainsAny(column, "\"\\\n\r") {
		return "", fmt.Errorf("gen: column %q contains a character that cannot appear in a struct tag", column)
	}
	return "`json:\"" + column + "\"`", nil
}

// enumFields returns e's enum fields in declaration order.
//
// It is a function rather than an {{if}} in the template for spec §5.1's
// reason: selecting which fields get a generated type is a decision, and
// decisions live in Go where a test can reach them. Declaration order, never
// a map: the emitted order is part of the output's bytes (spec §5.3).
func enumFields(e *ir.Entity) []*ir.Field {
	var out []*ir.Field
	for _, f := range e.Fields {
		if f.Type == ir.FieldTypeEnum {
			out = append(out, f)
		}
	}
	return out
}

// importBlock renders paths as a Go import declaration, standard library
// first, then everything else, one blank line between the two groups and
// each group sorted.
//
// The grouping is done here and not left to the formatter on purpose.
// imports.Process with FormatOnly sorts within the blocks a file already
// has; it does not create them. Emitting the groups means the formatter's
// output is the same bytes it was handed, which is what makes formatting
// idempotent and the golden files stable.
//
// The leading newline separates the block from the package clause; an empty
// path list renders nothing at all rather than an empty import ().
func importBlock(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}

	seen := make(map[string]bool, len(paths))
	var std, ext []string
	for _, p := range paths {
		if p == "" {
			return "", fmt.Errorf("gen: empty import path in %v", paths)
		}
		if seen[p] {
			return "", fmt.Errorf("gen: import path %q listed twice in %v", p, paths)
		}
		seen[p] = true
		if isStdlibImport(p) {
			std = append(std, p)
		} else {
			ext = append(ext, p)
		}
	}
	sort.Strings(std)
	sort.Strings(ext)

	var b strings.Builder
	b.WriteString("\nimport (\n")
	for _, p := range std {
		b.WriteString("\t" + strconv.Quote(p) + "\n")
	}
	if len(std) != 0 && len(ext) != 0 {
		b.WriteString("\n")
	}
	for _, p := range ext {
		b.WriteString("\t" + strconv.Quote(p) + "\n")
	}
	b.WriteString(")\n")
	return b.String(), nil
}

// isStdlibImport reports whether path names a standard library package.
//
// The general test is the toolchain's own: a standard library path's first
// element contains no dot, because a module path's does. That stopped being
// the whole story once a generated package could import another one (step
// 6, hooks importing model): a module path is not required to contain a
// dot -- spec §6.1's own `lapigo new myapp` example does not -- and "myapp"
// is indistinguishable from a standard-library-shaped path by the dot rule
// alone. A path through genRoot is unconditionally the target project's
// own, whatever its module happens to be called, because the standard
// library has no "internal/gen" of anyone's: importsForFieldType and
// hooksEntityImports never produce one, spec §2.3 allows only the standard
// library and pgx besides it, and neither of those ever contains this
// project's own output directory.
func isStdlibImport(path string) bool {
	if strings.Contains(path, "/"+genRoot+"/") {
		return false
	}
	first := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}
	return !strings.Contains(first, ".")
}
