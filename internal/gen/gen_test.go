package gen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"text/template"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/parse"
	"github.com/cdhdt/lapigo/internal/source"
	"github.com/cdhdt/lapigo/internal/validate"
)

// testModulePath is the modulePath every test in this package that calls
// Generate or plan supplies, standing in for a real project's go.mod module
// line. It carries a dot in its first segment for realism -- most real
// module paths do (the "github.com/..." convention) -- even though
// isStdlibImport no longer needs one: a bare name such as spec §6.1's own
// `lapigo new myapp` example is covered by its genRoot special case (see
// isStdlibImport's own doc comment), and TestIsStdlibImport pins that case
// directly.
const testModulePath = "example.com/lapigotest"

// loadFixture parses, validates and freezes testdata/<name>.yaml, failing the
// test if any stage reports anything. Generate's contract is a frozen,
// validated *ir.Schema (spec §2.1: load, parse, resolve and validate all sit
// above it), so a fixture that does not reach that state would be testing a
// precondition the pipeline never produces -- the same guard
// internal/ddl/golden_test.go applies to its own corpus.
func loadFixture(t *testing.T, name string) *ir.Schema {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("testdata", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f := source.File{Name: "lapigo.yaml", Src: src}

	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("fixture %s.yaml must parse cleanly; got:\n%s", name, parseDiags.Render(f))
	}
	diags := validate.Validate(schema, f.Name)
	if err := diags.Err(); err != nil {
		t.Fatalf("fixture %s.yaml must carry no error-severity diagnostics; got:\n%s", name, diags.Render(f))
	}
	if err := schema.Freeze(); err != nil {
		t.Fatalf("fixture %s.yaml must freeze: %v", name, err)
	}
	return schema
}

// TestGenerate_FileSet pins the exact set of paths Generate returns for the
// full fixture. The map's key set is a contract in its own right: it is what
// step 9 writes to disk and what the lock records, so a template added
// without a matching path -- or a path that moves -- has to show up as a
// failure here, not as a surprise in someone's working tree.
func TestGenerate_FileSet(t *testing.T) {
	files, err := Generate(loadFixture(t, "full"), testModulePath)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	want := []string{
		"internal/gen/model/optional.go",
		"internal/gen/model/article.go",
		"internal/gen/model/user.go",
		"internal/gen/hooks/error.go",
		"internal/gen/hooks/article.go",
		"internal/gen/hooks/user.go",
	}
	got := make([]string, 0, len(files))
	for path := range files {
		got = append(got, path)
	}
	assertStringSetsEqual(t, "Generate's file set", got, want)
}

// TestGenerate_NilSchema pins the error text for the one input Generate can
// be handed that is not a schema at all. The message is asserted whole: an
// error string is part of the contract (CLAUDE.md).
func TestGenerate_NilSchema(t *testing.T) {
	files, err := Generate(nil, testModulePath)
	if files != nil {
		t.Errorf("Generate(nil, testModulePath) returned %d files, want none", len(files))
	}
	if err == nil {
		t.Fatal("Generate(nil, testModulePath) returned a nil error, want one")
	}
	const want = "gen: Generate called on a nil schema"
	if err.Error() != want {
		t.Errorf("Generate(nil, testModulePath) error = %q, want %q", err.Error(), want)
	}
}

// assertStringSetsEqual compares two string sets in BOTH directions and
// reports each side's surplus separately. Set equality, never a subset
// check: a subset in one direction alone passes when the other side has
// grown, which is exactly the drift spec §5.6 requires this style of test to
// catch. Both slices are sorted in place; neither is assumed sorted.
func assertStringSetsEqual(t *testing.T, what string, got, want []string) {
	t.Helper()

	inWant := make(map[string]bool, len(want))
	for _, s := range want {
		inWant[s] = true
	}
	inGot := make(map[string]bool, len(got))
	for _, s := range got {
		inGot[s] = true
	}

	var surplus, missing []string
	for _, s := range got {
		if !inWant[s] {
			surplus = append(surplus, s)
		}
	}
	for _, s := range want {
		if !inGot[s] {
			missing = append(missing, s)
		}
	}
	sort.Strings(surplus)
	sort.Strings(missing)

	if len(surplus) != 0 {
		t.Errorf("%s contains %v, which nothing predicts", what, surplus)
	}
	if len(missing) != 0 {
		t.Errorf("%s is missing %v, which is predicted and absent", what, missing)
	}
}

// TestNewTemplate_MissingKeyIsAnError is spec §5.1's mandatory
// Option("missingkey=error"), proved rather than asserted.
//
// Without the option a mistyped key renders the string "<no value>" into
// generated Go and the run succeeds -- a wrong answer delivered silently,
// which is the failure mode this project least tolerates. The companion test
// below shows exactly that happening when the option is absent, so this one
// cannot pass for some other reason.
func TestNewTemplate_MissingKeyIsAnError(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/probe.tmpl": &fstest.MapFile{
			Data: []byte(`{{define "probe"}}type {{.gonome}} struct{}{{end}}`),
		},
	}

	tmpl, err := newTemplate(fsys, "templates/*.tmpl")
	if err != nil {
		t.Fatalf("newTemplate: %v", err)
	}

	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "probe", map[string]any{"goName": "Article"})
	if err == nil {
		t.Fatalf("a mistyped key rendered %q instead of failing", buf.String())
	}
	const want = `template: probe.tmpl:1:25: executing "probe" at <.gonome>: map has no entry for key "gonome"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestMissingKeyWithoutTheOption_RendersNoValue records what the option
// prevents, so that the test above is known to be catching something.
//
// This constructs the template WITHOUT newTemplate on purpose: it is the
// only way to show the silent failure, and showing it is the point. If this
// ever stops rendering "<no value>", text/template's default has changed and
// the reasoning in newTemplate's doc comment needs revisiting -- which is a
// thing to be told about, not to discover.
func TestMissingKeyWithoutTheOption_RendersNoValue(t *testing.T) {
	tmpl, err := template.New("probe").Parse(`type {{.gonome}} struct{}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"goName": "Article"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	const want = "type <no value> struct{}"
	if buf.String() != want {
		t.Errorf("rendered %q, want %q", buf.String(), want)
	}
}

// TestRender_TypoInARealTemplateFails takes the production model template,
// introduces a deliberate typo in a field selector, and asserts that
// rendering it stops with an error rather than emitting a file.
//
// The templates are executed against a struct, where a mistyped selector is
// an execution error whatever the missingkey setting is -- this test pins
// that the data shape actually is a struct, which is what makes that true.
// The option above covers the map case; between them, no typo in a template
// can reach a generated file.
func TestRender_TypoInARealTemplateFails(t *testing.T) {
	header, err := templatesFS.ReadFile("templates/header.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	model, err := templatesFS.ReadFile("templates/model_entity.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	const correct = "{{.GoName}} is the generated model"
	const typo = "{{.GoNmae}} is the generated model"
	if !bytes.Contains(model, []byte(correct)) {
		t.Fatalf("templates/model_entity.tmpl no longer contains %q, so this test is mutating nothing", correct)
	}
	mutated := bytes.Replace(model, []byte(correct), []byte(typo), 1)

	tmpl, err := newTemplate(fstest.MapFS{
		"templates/header.tmpl":       &fstest.MapFile{Data: header},
		"templates/model_entity.tmpl": &fstest.MapFile{Data: mutated},
	}, "templates/*.tmpl")
	if err != nil {
		t.Fatalf("newTemplate: %v", err)
	}

	files, err := plan(loadFixture(t, "full"), testModulePath)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var article *OutputFile
	for i := range files {
		if files[i].Path == "internal/gen/model/article.go" {
			article = &files[i]
			break
		}
	}
	if article == nil {
		t.Fatalf("plan did not produce internal/gen/model/article.go: %v", files)
	}

	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, article.Template, article.Data)
	if err == nil {
		t.Fatalf("a mistyped field selector rendered %d bytes instead of failing", buf.Len())
	}
	if !strings.Contains(err.Error(), "can't evaluate field GoNmae") {
		t.Errorf("error = %q, want it to name the mistyped field GoNmae", err.Error())
	}
}

// TestGenerate_IsDeterministic is spec §5.3: the same schema in,
// byte-identical files out, twenty times over. `make check` runs the suite
// under -race, which is the other half of the assertion.
//
// Twenty rather than two because the source this guards against is Go's
// randomised map iteration order, and a two-element map agrees with itself
// often enough that two runs prove very little.
func TestGenerate_IsDeterministic(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			schema := loadFixture(t, name)

			first, err := Generate(schema, testModulePath)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			for i := 2; i <= 20; i++ {
				again, err := Generate(schema, testModulePath)
				if err != nil {
					t.Fatalf("Generate, run %d: %v", i, err)
				}

				paths := make([]string, 0, len(again))
				for p := range again {
					paths = append(paths, p)
				}
				firstPaths := make([]string, 0, len(first))
				for p := range first {
					firstPaths = append(firstPaths, p)
				}
				assertStringSetsEqual(t, fmt.Sprintf("run %d's file set", i), paths, firstPaths)

				sort.Strings(paths)
				for _, p := range paths {
					if !bytes.Equal(first[p], again[p]) {
						t.Fatalf("run %d produced different bytes for %s:\n got:\n%s\nwant:\n%s",
							i, p, again[p], first[p])
					}
				}
			}
		})
	}
}

// at pairs a value with a position no test asserts on. The hand-built IR
// nodes below exist to reach code paths a schema cannot express -- a
// newline in an entity name, an out-of-range FieldType -- and their spans
// are never rendered, so a placeholder position is honest rather than
// misleading.
func at(v string) source.At[string] {
	return source.NewAt(v, source.Pos{Line: 1, Column: 1}, source.Pos{Line: 1, Column: 1 + len(v)})
}

// TestComment is spec §5.1's comment sanitisation, on the cases that
// actually break a file.
//
// strconv.Quote does not cover a comment position: a value containing a
// newline ends the "//" and drops whatever followed it into the file as Go
// code. Line terminators become one space each; other control characters are
// dropped, because a lone control character carries no width a reader would
// miss.
func TestComment(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain text is untouched", "article", "article"},
		{"a newline becomes a space", "art\nicle", "art icle"},
		{"a carriage return becomes a space", "art\ricle", "art icle"},
		{"CRLF becomes two spaces", "art\r\nicle", "art  icle"},
		{"a tab becomes a space", "art\ticle", "art icle"},
		{"a vertical tab becomes a space", "art\vicle", "art icle"},
		{"a form feed becomes a space", "art\ficle", "art icle"},
		{"a NUL is dropped", "art\x00icle", "article"},
		{"a DEL is dropped", "art\x7ficle", "article"},
		{"an escape is dropped", "art\x1bicle", "article"},
		{"multi-byte text survives", "café", "café"},
		{"the empty string stays empty", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := comment(c.in); got != c.want {
				t.Errorf("comment(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestGoString pins that every user string reaching a Go literal is quoted
// with its own delimiters escaped (spec §5.1). The cases are the ones that
// would otherwise terminate the literal and continue as code.
func TestGoString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"draft", `"draft"`},
		{`in "progress"`, `"in \"progress\""`},
		{`back\slash`, `"back\\slash"`},
		{"new\nline", `"new\nline"`},
		{"tab\there", `"tab\there"`},
		{"nul\x00", `"nul\x00"`},
	}

	for _, c := range cases {
		if got := goString(c.in); got != c.want {
			t.Errorf("goString(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// TestJSONTag pins the tag's exact bytes -- the JSON key is Field.Column,
// there is no omitempty, and the tag is a raw string literal (spec §6.9.2).
func TestJSONTag(t *testing.T) {
	got, err := jsonTag("author_id")
	if err != nil {
		t.Fatalf("jsonTag: %v", err)
	}
	want := "`json:\"author_id\"`"
	if got != want {
		t.Errorf("jsonTag = %s, want %s", got, want)
	}
}

// TestJSONTag_RejectsCharactersThatWouldEndTheTag pins that generation stops
// rather than emitting a tag that has silently swallowed the rest of the
// struct. The parser's identifier grammar cannot produce any of these today,
// which is exactly why the check is here: if the grammar widens, this fails
// instead of the output.
func TestJSONTag_RejectsCharactersThatWouldEndTheTag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"id`json:\"x\"`", "gen: column \"id`json:\\\"x\\\"`\" contains a backtick, which cannot appear in a struct tag"},
		{`id"`, `gen: column "id\"" contains a character that cannot appear in a struct tag`},
		{`id\`, `gen: column "id\\" contains a character that cannot appear in a struct tag`},
		{"id\n", `gen: column "id\n" contains a character that cannot appear in a struct tag`},
		{"id\r", `gen: column "id\r" contains a character that cannot appear in a struct tag`},
	}

	for _, c := range cases {
		got, err := jsonTag(c.in)
		if got != "" {
			t.Errorf("jsonTag(%q) = %s, want no tag", c.in, got)
		}
		if err == nil {
			t.Fatalf("jsonTag(%q) returned a nil error, want one", c.in)
		}
		if err.Error() != c.want {
			t.Errorf("jsonTag(%q) error = %q, want %q", c.in, err.Error(), c.want)
		}
	}
}

// TestImportBlock pins the whole rendered block: the standard library first,
// then everything else, one blank line between the groups, each group
// sorted, and a leading newline that separates the block from the package
// clause.
//
// The grouping is done here rather than left to the formatter because
// imports.Process with FormatOnly sorts within the blocks a file already
// has and does not create them (spec §5.2).
func TestImportBlock(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{
			name: "no imports render nothing at all, never an empty import ()",
			in:   nil,
			want: "",
		},
		{
			name: "one standard library package",
			in:   []string{"time"},
			want: "\nimport (\n\t\"time\"\n)\n",
		},
		{
			name: "one module package, no leading blank line inside the block",
			in:   []string{"github.com/jackc/pgx/v5/pgtype"},
			want: "\nimport (\n\t\"github.com/jackc/pgx/v5/pgtype\"\n)\n",
		},
		{
			name: "two groups, sorted, separated by one blank line",
			in:   []string{"time", "github.com/jackc/pgx/v5/pgtype", "encoding/json"},
			want: "\nimport (\n\t\"encoding/json\"\n\t\"time\"\n\n\t\"github.com/jackc/pgx/v5/pgtype\"\n)\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := importBlock(c.in)
			if err != nil {
				t.Fatalf("importBlock: %v", err)
			}
			if got != c.want {
				t.Errorf("importBlock(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestImportBlock_RejectsMalformedSets pins the two inputs that would render
// a file that does not compile: a duplicate import path, which Go rejects
// outright, and an empty one, which renders as an unnamed "".
func TestImportBlock_RejectsMalformedSets(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"time", "time"}, `gen: import path "time" listed twice in [time time]`},
		{[]string{"time", ""}, `gen: empty import path in [time ]`},
	}

	for _, c := range cases {
		got, err := importBlock(c.in)
		if got != "" {
			t.Errorf("importBlock(%v) = %q, want no block", c.in, got)
		}
		if err == nil {
			t.Fatalf("importBlock(%v) returned a nil error, want one", c.in)
		}
		if err.Error() != c.want {
			t.Errorf("importBlock(%v) error = %q, want %q", c.in, err.Error(), c.want)
		}
	}
}

// TestIsStdlibImport pins the standard library test: a module path's first
// element contains a dot and a standard library path's does not. Both sides
// are covered, including the single-element forms where the rule is easiest
// to get backwards, and the genRoot special case a bare, dotless module name
// (spec §6.1's own `lapigo new myapp` example) needs -- see the doc comment.
func TestIsStdlibImport(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"time", true},
		{"encoding/json", true},
		{"crypto/rand", true},
		{"github.com/jackc/pgx/v5/pgtype", false},
		{"gopkg.in/yaml.v3", false},
		{"example.com", false},
		{"myapp/internal/gen/model", false},
		{"myapp/internal/gen/hooks", false},
	}

	for _, c := range cases {
		if got := isStdlibImport(c.in); got != c.want {
			t.Errorf("isStdlibImport(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestEnumFields pins that enum fields come back in declaration order and
// that a non-enum field never does. Declaration order, never a map: the
// emitted order is part of the output's bytes (spec §5.3).
func TestEnumFields(t *testing.T) {
	e := &ir.Entity{
		Name:   "task",
		GoName: "Task",
		Table:  "tasks",
		Fields: []*ir.Field{
			{Name: at("id"), GoName: "ID", Column: "id", Type: ir.FieldTypeUUID, PK: true},
			{Name: at("state"), GoName: "State", Column: "state", Type: ir.FieldTypeEnum, EnumGoType: "TaskState"},
			{Name: at("title"), GoName: "Title", Column: "title", Type: ir.FieldTypeString},
			{Name: at("mood"), GoName: "Mood", Column: "mood", Type: ir.FieldTypeEnum, EnumGoType: "TaskMood"},
		},
	}

	got := enumFields(e)
	want := []string{"TaskState", "TaskMood"}
	if len(got) != len(want) {
		t.Fatalf("enumFields returned %d fields, want %d", len(got), len(want))
	}
	for i, f := range got {
		if f.EnumGoType != want[i] {
			t.Errorf("enumFields[%d].EnumGoType = %q, want %q", i, f.EnumGoType, want[i])
		}
	}

	if enumFields(&ir.Entity{Name: "empty", GoName: "Empty", Table: "empties"}) != nil {
		t.Error("enumFields on an entity with no fields returned a non-nil slice")
	}
}

// TestGenerate_SanitisesUserStringsInComments is the comment rule end to
// end, on an IR built by hand.
//
// The parser's identifier grammar cannot produce a name containing a
// newline, and that is the reason to test it here rather than through a
// fixture: the two checks are meant to fail independently (spec §5.1), and
// a defence that is only ever exercised through the thing it is defending
// against is not a defence anyone has measured. Without the sanitisation,
// "art\nicle" would end the comment line and leave `icle" (table "articles").`
// in the file as Go code -- so this test fails at the formatter, loudly,
// which is the behaviour spec §5.2 requires.
func TestGenerate_SanitisesUserStringsInComments(t *testing.T) {
	schema := &ir.Schema{Entities: []*ir.Entity{{
		Name:   "art\nicle",
		GoName: "Article",
		Table:  "arti\ncles",
		Fields: []*ir.Field{
			{Name: at("id"), GoName: "ID", Column: "id", Type: ir.FieldTypeBigint, PK: true},
		},
	}}}

	files, err := Generate(schema, testModulePath)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	const path = "internal/gen/model/art\nicle.go"
	got, ok := files[path]
	if !ok {
		t.Fatalf("no output at %q; got %v", path, files)
	}

	want := `// Code generated by lapigo. DO NOT EDIT.

package model

// Article is the generated model for entity "art icle" (table "arti cles").
//
// It is output only: the store fills it from a scanned row and a handler
// marshals it. Every field carries an explicit json tag naming its column, and
// no field is omitempty -- a NULL column must serialise as null rather than
// vanish from the object (spec §6.9.2).
type Article struct {
	ID int64 ` + "`json:\"id\"`" + `
}
`
	if string(got) != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, want)
	}
}
