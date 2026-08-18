# Phase 1 — Core generator design

Status: **proposed**, revision 2
Date: 2026-08-15

Revision 2 follows an adversarial review of revision 1 conducted against live
PostgreSQL 17.10 and 18.6, pgx v5.10.0, Go 1.26.5 and goccy/go-yaml v1.19.2.
That review found revision 1 unsafe to implement from. §12 records what changed
and why, so the reasoning is not rediscovered later.

---

## 1. Goal

**`lapigo new` → `lapigo gen` → apply the SQL → `go run` → a working API.**

That end-to-end path is the deliverable. Revision 1 deferred migrations and
therefore generated a store and handlers for tables nothing created; the output
could not be run, and could not be tested against a real database. Phase 1 now
owns the schema it queries.

Everything else is scoped to serve that path and nothing more.

### In scope

- `lapigo.yaml`: entities, fields, scalar types, enums, `belongsTo`, one sort
  spec per entity, a filter whitelist, endpoint selection.
- Parse → IR → validate, with positioned diagnostics.
- **DDL generation**: `CREATE TABLE`, constraints, and the indexes the declared
  sorts and filters require.
- Templates → `internal/gen/`, formatted, deterministic, atomically written.
- Generated CRUD: `list`, `get`, `create`, `update`, `delete`.
- Cursor pagination, **uniform-direction sorts only** (see §7.1).
- `lapigo new`, `lapigo gen`.

### Deferred, with the reason

| Deferred | To | Why |
|---|---|---|
| Mixed-direction sorts (`-created_at, +id`) | 1.5 | Cannot use a plain row-value comparison; needs a guarded predicate or `UNION ALL`, and must be benchmarked before it ships. |
| Request-selectable sorts | 1.5 | Each permitted sort needs its own tiebreaker and its own index. Phase 1 declares exactly one sort per entity. |
| Relation expansion in responses | 1.5 | Must be a JOIN or a batched `IN`. Until it is specified and proven N+1-free, phase 1 exposes the foreign key scalar only. |
| HMAC-signed cursors | 1.5 | Defence in depth, not a vulnerability fix (§7.4). Strict typed decoding carries phase 1. |
| Schema diffing and versioned migrations | 2 | Phase 1 emits an initial migration only, and never rewrites it. |
| Auth, OpenAPI, Redis cache | 3, 4, 5 | Unchanged. |
| Composite primary keys, many-to-many, soft delete, file uploads | later | — |

---

## 2. Architecture

### 2.1 Pipeline

```
lapigo.yaml
   │
   ├─ 1. Load        read the schema file
   ├─ 2. Parse       YAML → goccy AST, positions preserved
   ├─ 3. Resolve     AST → IR, defaults expanded, relations resolved
   ├─ 4. Validate    on the IR, accumulating diagnostics
   ├─ 5. Verify      read .lapigo.lock, detect hand-edits — FAIL FAST HERE
   ├─ 6. Plan        compute the set of output files and their imports
   ├─ 7. Render      IR + templates → map[string][]byte, in memory
   ├─ 8. Format      imports.Process per file
   └─ 9. Write       staging directory, swap, rewrite the lock
```

```go
func Generate(s *ir.Schema) (map[string][]byte, error)
```

Steps 6–8 are that function: no filesystem access at all, not "none except the
read in step 1". Load, parse, resolve and validate sit above it; verify and
write sit below. This boundary is what makes golden tests, determinism tests and
type-checking fast and race-free, and it guarantees that a template failure
leaves the output tree untouched.

**Verify runs before render**, not inside write. Discovering a hand-edited file
after twenty templates have rendered wastes the work and reports the error late.

### 2.2 The IR

Templates consume the IR and only the IR — never YAML, never the AST, never a
`map[string]any`. A new input (Postgres introspection) produces the same IR and
touches no template; a new output (a TypeScript client) consumes it and touches
no parser.

```go
type Schema struct {
    Entities []*Entity           // sorted by name
}

type Entity struct {
    Name      string             // as written
    NameSpan  source.Span        // where the name was written
    GoName    string             // validated Go identifier
    Table     string
    TableSpan source.Span        // zero when `table:` was defaulted
    Fields    []*Field           // declaration order
    PK        *Field
    Sort      SortSpec
    Filters   []Filter
    Relations []Relation
    Endpoints []Endpoint
}

type Field struct {
    Name       source.At[string]
    GoName     string
    Column     string
    Type       FieldType         // resolved, not a string
    Nullable   bool
    Unique     bool
    PK         bool
    ReadOnly   bool              // never accepted from a request
    Immutable  bool              // accepted on create, rejected on update
    Version    bool              // optimistic concurrency column
    Max        *int              // string length constraint
    EnumGoType string            // set only for FieldTypeEnum, e.g. "ArticleStatus"
    EnumValues []source.At[string]
    Default    *DefaultValue
}

// Derived, never stored. See below.
func (f *Field) GoType() string
func (f *Field) PgType() string

type SortSpec struct {
    Keys []SortKey               // last key resolves to a unique field
    Desc bool                    // ONE direction for the whole spec (§7.1)
}

type SortKey struct {
    Field *Field                 // RESOLVED pointer, not a name
    Span  source.Span            // the entry in `sort:`, sign included
}

type Filter struct {
    Field *Field                 // resolved
    Op    FilterOp               // Eq only in phase 1
    Span  source.Span            // the entry in `filters:`
}

type Relation struct {
    Name       string            // "author"
    NameSpan   source.Span
    GoName     string            // "Author"
    Target     *Entity           // resolved
    TargetSpan source.Span       // the `target:` value; zero when `target:` was omitted
    Column     string            // "author_id"
    GoType     string            // from the target's PK
    Nullable   bool
    OnDelete   string            // "RESTRICT" default
}

type Endpoint struct {
    Kind EndpointKind            // List, Get, Create, Update, Delete
    Path string
}
```

**`Relation.TargetSpan` is the zero `Span` when the schema omitted `target:`
entirely.** The parser reports that omission itself, at the relation's own name,
and never records a span for a value that was never written — which is exactly
the "zero `Span` means no position" rule `source.Span` defines. A future
diagnostic that wants to blame a *missing* `target:` must therefore anchor on
`NameSpan`, not on `TargetSpan`; reaching for the zero span would print
`lapigo.yaml:0:0:`, the misleading header §4 exists to prevent.

**Sort keys and filters hold resolved `*Field` pointers, not names.** A template
rendering a comparison needs the field's Go type, column and nullability; a
name would force a lookup inside the template, which §5.1 forbids. An IR that
still requires resolution is not resolved.

**The collections are `[]*Entity` and `[]*Field`, not slices of values**, and
this is not a style preference. A `*Field` taken into a `[]Field` is invalidated
by any later `append`, and — far worse — **sorting the slice silently retargets
it**. This spec mandates that `Schema.Entities` be sorted by name, so a
`Relation.Target` resolved before that sort would end up pointing at a different
entity: no crash, no nil, no race detector hit, just a foreign key emitted to the
wrong table. Pointer slices make identity survive both reallocation and
reordering.

`Schema.Freeze()` asserts the invariant after resolution — every `Entity.PK` is
an element of that entity's own `Fields`, every `SortKey.Field` and
`Filter.Field` belongs to its entity, every `Relation.Target` is an element of
`Schema.Entities` — and there is a test that resolves, then appends and sorts,
and checks identity survives. A documented "do not append" rule is not a fix.

**Every IR node a diagnostic can blame carries its own span.** An earlier
revision gave `SortKey` a start position with no end, and gave `Entity`,
`Filter` and `Relation` nothing at all. The consequence showed up as soon as the
validator was written: a diagnostic about a filter pointed at the *filtered
field's declaration* instead of the offending `filters:` entry, sending the
reader to the wrong line — the precise failure the whole positions effort exists
to prevent. Reconstructing a span downstream is guesswork, because only the
parser saw the written form, and a quoted or sign-prefixed token is wider than
the value it carries.

**`GoType` and `PgType` are computed methods, never stored fields.** Revision 1
stored them as strings *alongside* the `FieldType` they derive from, which made
`Field{Type: FieldTypeInt, GoType: "string", PgType: "double precision"}`
constructible with nothing objecting — a Go `string` bound to a
`double precision` column. The only genuine inputs to the resolved type are
`Type`, `Nullable`, `Max` (for `varchar(n)`) and `EnumGoType` (which needs the
entity name, hence cannot live on `FieldType`). Storing those and deriving the
rest makes divergence unrepresentable rather than merely discouraged.

**Imports are a property of a file, not an entity.** Step 6 produces a
`[]OutputFile{Path, Package, Imports, Data}`; each output file carries its own
import set. `Entity` has no `Imports` field — an entity spans four packages with
different needs.

### 2.3 Dependencies

Generator: `github.com/goccy/go-yaml`, `golang.org/x/tools/imports`, stdlib.
Generated code: stdlib and `github.com/jackc/pgx/v5`. Nothing else.

`gopkg.in/yaml.v3` was archived on 1 April 2025 and must not be used.
`go.yaml.in/yaml/v3` is its continuation but is explicitly frozen to security
fixes, with development on a v4 still at release-candidate stage. goccy is
chosen for one specific reason: **it is the only option exposing a structured,
positioned syntax error** (`*yaml.SyntaxError`, `errors.As`-compatible since
v1.15.0). yaml.v3 does expose `Line` and `Column` on every `yaml.Node`, so the
honest framing is that goccy wins on *syntax* diagnostics, not on positions
generally.

Recorded risk: goccy has a single primary maintainer, and the whole diagnostic
system depends on it. The AST walk is confined to one package so that a
replacement would touch the parser only.

---

## 3. Schema format

```yaml
entities:
  article:
    table: articles                    # optional; default is name + "s"
    fields:
      id:         { type: uuid, pk: true }
      title:      { type: string, required: true, max: 200 }
      body:       { type: text }
      status:     { type: enum, values: [draft, published], required: true }
      slug:       { type: string, unique: true, readonly: true }
      author:     { type: belongsTo, target: user, on_delete: restrict }
      created_at: { type: timestamp, required: true, default: now, immutable: true }
      version:    { type: int, version: true }
    sort: [-created_at, -id]           # ONE direction for the whole spec
    filters: [status, author]
    endpoints: [list, get, create, update, delete]
```

### 3.1 Field options — the complete set

| Option | Meaning |
|---|---|
| `type` | Required. See §3.2. |
| `pk` | Primary key. Exactly one per entity in phase 1. |
| `required` | `NOT NULL`, and required in create input. |
| `unique` | `UNIQUE` constraint. Makes the field eligible as a sort tiebreaker. |
| `max` | Maximum length for `string`. Emits `varchar(n)` **and** a validation check. |
| `values` | Enum members. Emits a `CHECK` constraint. |
| `target` | `belongsTo` target entity. |
| `on_delete` | `restrict` (default), `cascade`, `set_null`. |
| `default` | `now`, `uuid`, or a literal. Excludes the field from create input. |
| `readonly` | Persisted and returned, **never accepted from a request**. |
| `immutable` | Accepted on create, rejected on update. |
| `version` | Optimistic concurrency column. At most one per entity. |

There is no `index:` option. Revision 1 had one, and it was a user-asserted
claim about a database lapigo neither created nor inspected — the validator
could be satisfied by editing YAML without an index existing. **The generator
derives every required index from the declared sort and filters** (§7.2) and
emits it as DDL. Nothing to assert, nothing to lie about.

### 3.2 Types

| YAML | Postgres | Go |
|---|---|---|
| `uuid` | `uuid` | `pgtype.UUID` |
| `string` | `text` or `varchar(max)` | `string` |
| `text` | `text` | `string` |
| `int` | `integer` | `int32` |
| `bigint` | `bigint` | `int64` |
| `float` | `double precision` | `float64` |
| `decimal` | `numeric` | `pgtype.Numeric` |
| `bool` | `boolean` | `bool` |
| `timestamp` | `timestamptz` | `time.Time` |
| `date` | `date` | `time.Time` |
| `json` | `jsonb` | `json.RawMessage` |
| `enum` | `text` + `CHECK` | generated string type |

Nullable fields use the pointer form (`*string`) so that "absent" and "null" are
distinguishable.

`uuid` primary keys are generated in Go with `crypto/rand` (RFC 4122 v4, about
fifteen lines, no dependency), not by a database default, so create returns the
identifier without a round trip and the code stays portable.

### 3.3 Sort constraints, enforced by the validator

1. **One direction for the whole spec.** `[-created_at, -id]` is valid;
   `[-created_at, id]` is a phase 1 error with a hint pointing at 1.5. A
   uniform-direction sort maps to a row-value comparison, which is the only form
   verified to hold an index seek (§7.1).
2. **The last key must be unique** — resolved from `pk` or `unique`. Without a
   unique tiebreaker a keyset scan skips or duplicates rows whenever two records
   share a sort value.
3. **No key may be nullable.** SQL comparison against `NULL` yields unknown, so
   such rows vanish from every page after the seek applies, and a cursor holding
   a `NULL` loses everything after it. Invisible row loss in production is
   strictly worse than a build-time complaint.
4. **No key may be `decimal` or `json`.** `pgtype.Numeric` marshals to a bare
   JSON number, which any generic decoder routes through `float64`, defeating
   the exactness the type exists for. Allowed as fields and filters, not as sort
   keys.
5. **Mutable sort keys warn.** A row whose sort value changes can move relative
   to a live cursor. Inherent to keyset pagination, not a bug — a warning plus
   documentation, not an error.

### 3.4 Filters

A whitelist. A request selects *which* whitelisted column to filter on; it can
never *name* a column. Every identifier in generated SQL comes from the IR.
This is what makes injection through filter and sort parameters structurally
impossible rather than a matter of escaping.

Phase 1 supports equality only.

---

## 4. Diagnostics

### 4.1 Target output

```
lapigo.yaml:12:12: sort key "created_at" is not unique
   12 |     sort: [-created_at]
      |            ^^^^^^^^^^^
   the last sort key must be unique; add a second key such as `-id`,
   or mark `created_at` unique
```

Every error carries file, line, column, a caret span and an actionable hint.

The column is 12, not 11: with a four-space indent, the `-` of `-created_at`
sits at rune column 12. Revision 1 of this document printed 11 in the header
while its own caret row pointed at 12 — the implementation follows the
arithmetic, and `internal/diag`'s golden test is now the authority on this
format.

### 4.2 Tabs are rejected outright

goccy does not advance `Column` across tab characters in flow context,
under-counting by one per tab. No amount of correct tab expansion at render time
fixes a wrong input column.

YAML already forbids tabs for indentation. **lapigo rejects any tab in the
schema file**, with its own diagnostic, before parsing. The class of bug
disappears, and the renderer never needs a rune-index-to-display-column map.

### 4.3 Positions

Captured during the AST walk that builds the IR — the walk visits every node
anyway, so recording a position costs nothing.

`Pos` and `At[T]` live in **`internal/source`**, a leaf package importing nothing
but the standard library. Both the IR and the diagnostic renderer need to name a
position, and neither should import the other to do it — an earlier layout put
`Pos` in `ir`, which meant `diag` depended on the IR and the IR could never
report a diagnostic without a cycle.

```go
package source

type Pos struct{ Line, Column int } // 1-based, columns counted in RUNES
type At[T any] struct { Value T; Pos Pos; End Pos }
```

`At` carries an **end position, not just a start**. Recomputing the span as
`Pos.Column + utf8.RuneCountInString(Value)` at each call site is wrong for any
quoted scalar: `"created_at"` occupies twelve columns on the line while its value
is ten runes, so every caret over a quoted key would be short by the quotes.

`At[T]` is applied selectively, to the leaves validation actually blames:
identifiers, enum members, sort keys. A `Pos` on every node would force a
position onto synthetic and defaulted values that have none.

`At[T]` does break naive `cmp.Diff` in tests — the same objection used to reject
a universal `Pos` field. It is handled explicitly with a shared
`cmpopts.IgnoreTypes(ir.Pos{})` option used by every IR test, rather than left
as an inconsistency.

The escape hatch for diagnostics that blame a whole subtree is a
`map[string]Pos` keyed by goccy's own path format, which is **YAMLPath**:
`$.entities.article.sort[0]`, with a leading `$.` and single quotes around keys
containing `.` or `[`. Also: goccy's `token.Position.Offset` is a **1-based rune
offset**, not a byte offset, and must never index a `[]byte`.

### 4.4 One diagnostic type, accumulate, sort, print once

`*yaml.SyntaxError` is converted to the same `Diagnostic` the validators
produce, so both render in one format, sorted together by position. Validators
append and keep going; nothing stops at the first error. This is the
`go/scanner.ErrorList` shape.

An earlier revision claimed a file containing a syntax typo *and* a semantic
error would print both. **That is not achievable, and the claim was wrong.**
goccy returns a nil `*ast.File` on any syntax error — there is no partial AST to
walk, so no semantic diagnostic can exist for that file. The guarantee that
holds, and that is tested, is the *rendering* one: whatever their origin,
diagnostics interleave and sort into one report. A syntax error therefore ends
parsing for that file, and the user fixes it before seeing anything else. That
is the same contract a compiler offers, and it is honest.

Column arithmetic is in runes throughout, converted only at render time. An
accented identifier is enough to misplace a caret computed in bytes, and there
is an explicit test for it.

### 4.5 What the renderer must guarantee

**Rendering is multi-file.** `Render(srcs ...source.File)` looks each diagnostic
up by its `File`, and omits the snippet when that file is absent. A renderer
taking a single source renders every diagnostic against it regardless of which
file the diagnostic names — printing a line from `a.yaml` beneath a header that
says `b.yaml`. Confidently wrong output is worse than none.

**Control bytes from the schema never reach the terminal.** A schema file is
untrusted input: fetched from a template, pasted from an issue, checked into
someone else's repository. Echoing its bytes into a rendered snippet means
`\x1b]0;…\x07` rewrites the user's window title and `\x08` overwrites the
diagnostic lapigo just printed. Every rune for which `unicode.IsControl` holds,
plus the zero-width and line-separator ranges, is replaced by one visible
placeholder rune — one for one, so column arithmetic is unchanged.

**Ordering is total.** `File`, then `Line`, `Column`, `EndColumn`, `Severity`,
`Message`. Ordering on line and column alone leaves two diagnostics at the same
position in accumulation order, which is whatever the validator's traversal
produced — and §5.3 requires byte-identical output for identical input. A
diagnostic report is output.

**`Err()` is nil unless there is an error.** §3.3 rule 5 defines a warning that
must not stop generation, so the idiomatic `if err := diags.Err(); err != nil`
must not abort on warnings alone. A separate accessor exists for the caller that
wants `-Werror` behaviour.

**Alignment is exact for width-1 characters, and the documentation says so.**
Rune columns are the right choice — byte columns are worse — but a rune column
is not a display column: `名前` is two runes and four cells, and a combining mark
is a rune with no cell at all. The doc comments state the limitation rather than
claiming carets always land correctly, and a test records the known skew. Display
width is a 1.5 concern.

---

## 5. Generation

### 5.1 Templates

`text/template`, split across files, embedded with `embed.FS` and `ParseFS`.

- **`Option("missingkey=error")` is mandatory.** Without it a typo renders
  `<no value>` in silence.
- **Templates are nearly logic-free.** Type mapping, casing and naming live in
  Go, where they are unit-testable. Nested `{{if eq .Type "string"}}` chains are
  rejected in review.
- **Never `range` a Go map in a template.** The IR exposes sorted slices.
- **`strconv.Quote` every user string reaching a Go string literal.**
- **Sanitise every user string reaching a comment position.** A schema value
  containing a newline breaks the file from a comment, and `strconv.Quote` does
  not cover comments. Newlines are stripped; the validator rejects control
  characters in names.

### 5.2 Formatting

`golang.org/x/tools/imports.Process`, with import resolution **enabled**.

Revision 1 had the IR compute the import set and ran `FormatOnly: true` to save
the resolution cost. That was a micro-optimisation of build time — milliseconds
across a handful of files — bought with a permanent correctness risk:
`FormatOnly` neither adds a missing import nor removes an unused one, so any
error in the computed set becomes a compile error in the user's project with no
safety net. The correction pass is worth more than the milliseconds.

When rendered text fails to parse, the error presents the **unformatted** output
with line numbers, so the reported `line:col` points at something readable.
Generation then fails hard: no partial or unformatted Go is ever written.

A parse failure is *usually* a template bug. It is not always — user data
reaches Go source through comments and literals, which is why §5.1 sanitises and
§5.5 validates. The error message says "usually a template bug; check the
schema values quoted below".

### 5.3 Determinism

Same schema in, byte-identical files out.

| Source | Mitigation |
|---|---|
| `range` over a Go map | The IR exposes sorted slices; maps never reach a template. |
| Wall-clock in headers | **No timestamp in generated output.** A "generated at" comment diffs on every CI run. |
| Absolute paths | Relative to the module root. |
| Generator version string | Pinned at build time with `-ldflags -X`, never computed per run. |
| Schema file discovery order | Explicitly sorted, never left to filesystem enumeration. |

Tested by generating twenty times and asserting byte equality of every file,
under `-race`.

### 5.4 Writing

1. Render everything to memory. A failure anywhere aborts before touching disk.
2. Write into `internal/.lapigo-staging`. The leading dot matters: the Go
   toolchain ignores directories beginning with `.` or `_`, so a staging
   directory left by a crash does not break `go build ./...`. A plain sibling
   like `internal/gen-staging` would be compiled, producing duplicate package
   declarations.
3. Rename `internal/gen` to `internal/.lapigo-old`, rename staging into place,
   remove the old one.
4. Rewrite `.lapigo.lock`.

**Two renames are not one atomic operation, and this spec does not claim they
are.** A crash between them leaves no `internal/gen` and an orphaned
`.lapigo-old`, which breaks the user's build. The recovery path is explicit
rather than wished away: on startup, if `internal/gen` is missing and
`.lapigo-old` exists, restore it and report what happened. Orphaned staging
directories are swept on the same pass.

### 5.5 Hand-edit detection

`.lapigo.lock` records a SHA-256 per generated file plus the generator version.
It is **committed to version control** — a fresh clone must know what the
previous generation produced.

| Situation | Behaviour |
|---|---|
| File checksum differs from the lock | Stop, name the file, exit non-zero. `--force` overrides. |
| A file in `internal/gen` the lock has never seen | Stop. It is either a hand-added file or a stale artifact; the tool does not guess. |
| Lock absent, `internal/gen` absent | First run. Proceed. |
| Lock absent, `internal/gen` present | Stop. Nothing is known about that content, and rename-aside would destroy it. `--force` overrides. |

A hand-edit is almost always a signal that an extension point is missing. The
safe failure mode is to tell a human, never to delete their work.

Every generated file opens with the canonical marker, whose exact form matters
because tooling matches it by regexp (`^// Code generated .* DO NOT EDIT\.$`):

```go
// Code generated by lapigo. DO NOT EDIT.
```

### 5.6 Names: reject, never mangle

Collisions and invalid identifiers are validation errors with a position.

Mangling makes the generated Go name a function of the generator version: if v1
resolves a collision to `UserType2` and v2 to `UserType_`, every consumer breaks
silently on upgrade. It also hides schema bugs — two fields colliding after
export-casing are nearly always a copy-paste mistake.

Checked across the whole package, not per file: Go keywords and predeclared
identifiers; leading digits and empty results; case collisions after export
(`user_id` and `userId` both yield `UserID`); a field colliding with a generated
method (`validate` → `Validate`); collision with an identifier the generated
package already exports.

---

## 6. The generated project

### 6.1 Two commands

```
lapigo new myapp     # once: go.mod, cmd/api/main.go, lapigo.yaml, .gitignore
lapigo gen           # repeatedly: internal/gen/ and migrations/0001_init.sql
```

```
myapp/
├── lapigo.yaml               # yours
├── go.mod                    # yours
├── cmd/api/main.go           # yours (scaffolded once, never touched again)
├── internal/
│   ├── gen/                  # lapigo, rewritten in full
│   │   ├── model/            structs, enums, input types
│   │   ├── store/            pgx queries, cursor, keyset
│   │   ├── httpapi/          handlers, decoding, errors, router
│   │   └── hooks/            interfaces and no-op implementations
│   └── app/                  # yours: hook implementations, custom routes
├── migrations/
│   └── 0001_init.sql         # lapigo, written once, NEVER rewritten
└── .lapigo.lock              # lapigo, committed
```

`0001_init.sql` is written only if it does not exist. If the schema changes
afterwards, `lapigo gen` warns that the migration is now behind and that
diffing arrives in phase 2; it does not silently rewrite applied history.

### 6.2 Four escape hatches

1. **Hooks** — before and after an operation (§6.3).
2. **Custom routes** — the generated router returns a `*http.ServeMux`.
   Note: Go's `ServeMux` **panics at registration** on a conflicting pattern,
   and wildcard names are irrelevant to conflict detection, so
   `GET /items/{userID}` conflicts with a generated `GET /items/{id}`. This is
   startup-time failure, which is the right behaviour, and it is documented so
   nobody discovers it in production. More specific patterns
   (`/items/count` alongside `/items/{id}`) are fine.
3. **Typed store queries** — `Store.List(ctx, ListQuery)` where `ListQuery` is a
   generated struct of whitelisted filters, cursor and limit. It accepts **no
   SQL fragments**. Anything beyond it is written with pgx directly, against the
   generated model types. A builder taking caller-supplied SQL would reopen the
   injection hole §3.4 closes structurally.
4. **Opt-out** — `endpoints: [list, get]` does not generate `create`.

### 6.3 Hooks

```go
// generated
type ArticleHooks interface {
    BeforeCreate(ctx context.Context, tx pgx.Tx, in *ArticleCreateInput) error
    AfterCreate(ctx context.Context, tx pgx.Tx, a *Article) error
    AfterCreateCommitted(ctx context.Context, a *Article)
    // ... same shape for Update and Delete
}

type NoopArticleHooks struct{}
func (NoopArticleHooks) BeforeCreate(context.Context, pgx.Tx, *ArticleCreateInput) error { return nil }
// ...
```

```go
// yours
type ArticleHooks struct{ gen.NoopArticleHooks }

func (h ArticleHooks) BeforeCreate(ctx context.Context, tx pgx.Tx, in *gen.ArticleCreateInput) error {
    in.Slug = slugify(in.Title)
    return nil
}
```

**The transaction is an explicit parameter.** Revision 1's signature took only a
`context.Context`, which made §6.4's "related writes inside the transaction"
impossible without smuggling the transaction through the context — untyped,
unfindable, and contradicting the rule that registration is explicit and typed.

**Embedding the no-op is what makes the interface evolvable.** A user implements
only what they need, and adding a hook method in a future release does not break
their build. Without it, every new hook is a breaking change for every user.

Registration is explicit and typed in `main.go`. No reflection, no discovery by
naming convention.

### 6.4 Transaction boundaries

| Hook | Context | Can abort | For |
|---|---|---|---|
| `BeforeX` / `AfterX` | inside the transaction | yes | validation, derived fields, related writes |
| `AfterXCommitted` | after commit | no | emails, webhooks, cache invalidation |

`AfterXCommitted` returns nothing and is **at-most-once**: a crash between
commit and hook loses it, and a failure inside it is logged, not retried. Stated
plainly because a user who needs at-least-once delivery must reach for an outbox
table, and the spec should not let them assume otherwise.

Phase 5 cache invalidation hangs off `AfterXCommitted`. Invalidating before
commit is a race that re-caches the pre-write state.

### 6.5 Input projection — what a client may set

This is the mass-assignment rule, stated as a rule rather than implied.

`CreateInput` contains every field **except**: the primary key, any field with a
`default:`, and any `readonly:` field. `UpdateInput` contains every field in
`CreateInput` **except** `immutable:` fields, with every member a pointer so
that "absent" and "explicitly null" are distinguishable.

JSON decoding uses `DisallowUnknownFields`: a request carrying a field the input
does not accept is a 400, not a silent drop. A client discovering that
`created_at` is ignored is better served by an error than by a surprise.

Without this rule a client could set `created_at` — the sort key — and insert
itself at an arbitrary position in every cursor page.

### 6.6 Update semantics

`PATCH` only; `PUT` is not generated. Absent means unchanged, explicit `null`
means set to null (rejected for `required` fields).

**Optimistic concurrency is opt-in via `version: true`.** When present, the
response carries an `ETag`, `PATCH` requires `If-Match`, and a mismatch is a
`409`. When absent, updates are last-write-wins — documented, not accidental.

An update that changes a sort key is permitted and warned about at generation
time (§3.3 rule 5).

### 6.7 Errors

One envelope, everywhere:

```json
{ "error": { "code": "validation_failed",
             "message": "title is required",
             "fields": { "title": "required" } } }
```

| Condition | Status | `code` |
|---|---|---|
| Malformed body, unknown field, bad cursor, bad limit | 400 | `bad_request`, `invalid_cursor` |
| Validation failure | 422 | `validation_failed` |
| Not found | 404 | `not_found` |
| Unique or FK violation (`23505`, `23503`) | 409 | `conflict` |
| `If-Match` mismatch | 409 | `version_conflict` |
| Anything else | 500 | `internal` |

A `500` body carries the code and a correlation identifier, never a driver
message, a query, a constraint name or a path. The detail is logged. This is
mechanism, not exhortation: handlers call one `respondError` helper, and a
`pgx` error never reaches a response except through the mapping above.

### 6.8 Resource bounds

Generated code, not advice in documentation:

- `http.MaxBytesReader` on every request body, default 1 MiB.
- `context.WithTimeout` on every query, default 5 s.
- Scaffolded `main.go` sets `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`
  and `IdleTimeout`.
- `limit` defaults to 20 and is capped at 100. Out-of-range, zero, negative or
  unparseable values are **rejected with 400** — not clamped. Silent clamping
  hides client bugs, and revision 1 specified both behaviours in one sentence.

An uncapped request body is the cheapest denial of service against the 4 vCPU
box this project targets.

---

## 7. Cursor pagination

### 7.1 Row-value comparison, uniform direction

```sql
SELECT ... FROM articles
WHERE (created_at, id) < ($1, $2)
ORDER BY created_at DESC, id DESC
LIMIT $3;
```

Postgres recognises the row constructor and turns it into a single index seek:
`Index Cond: (ROW(created_at, id) < ROW(...))`, measured at **4 buffers and
0.05 ms** at a page depth of 400 000 rows.

**Revision 1 mandated the expanded OR-chain and claimed identical index
requirements at no cost. That was wrong, and it was the most serious defect in
the document.** The same query as an OR-chain plans as a `Filter` with 400 001
rows removed: **5 076 buffers, 50.5 ms** — a thousandfold regression, on exactly
the dimension this project exists to solve. Postgres will not factor a leading
range condition out of the disjuncts, and PostgreSQL 18's OR-to-array transform
does not fire here.

Row-value comparison is only lexicographic under a *single* operator, so it
requires all sort keys to run in the same direction. Phase 1 therefore accepts
uniform-direction sorts only (§3.3 rule 1). Mixed direction needs a guarded
predicate (`k1 <= $1 AND (k1 < $1 OR (k1 = $1 AND k2 > $2))`, which restores the
`Index Cond` but degrades with the number of ties on `k1`) or a `UNION ALL` of
two seeks. Both are phase 1.5, and neither ships without measurement.

### 7.2 Indexes

Directions matter only up to **global inversion**, and the direction of an
equality-constrained column is irrelevant — an equality column yields a constant
equivalence class and generates no pathkey.

So a plain ascending index serves both directions of a uniform sort:

```sql
CREATE INDEX article_status_created_id_idx ON articles (status, created_at, id);
```

serves `ORDER BY created_at DESC, id DESC` by backward scan, at identical cost
(measured: 4 buffers either way). Revision 1 mandated direction-specific
indexes, which multiplied the index set and its write cost for nothing.

Column order is fixed: **equality filter columns first, then the sort keys in
order.** A btree consumes any number of leading equality conditions as its
prefix but only one range condition as the seek boundary; everything after
degrades to a filter.

`NULLS FIRST`/`NULLS LAST` is never emitted. The defaults align between
`ORDER BY` and `CREATE INDEX`, and a defensive `NULLS LAST` destroys every
keyset plan (measured: `Sort` + `Seq Scan`). §3.3 rule 3 bans nullable sort keys
anyway; this note exists so 1.5's `COALESCE` escape hatch does not reintroduce
the problem.

**Emitted index set, bounded by design:** one index per declared filter, prefixed
to the sort keys, plus one for the unfiltered sort. That is *N+1* indexes, not
*2^N*. A request combining two filters seeks on one and filters the rest — the
documented behaviour. A user who needs a specific combination declares it:

```yaml
indexes:
  - filters: [status, author]
```

### 7.3 Statements

One prepared statement per filter combination actually used. No
`($1 IS NULL OR col = $1)`.

Revision 1 used that idiom to keep the SQL text constant, because pgx caches
prepared statements by exact text. Measured, the idiom forces a `Filter` under a
generic plan — 10.6 ms against 0.16 ms on 500 000 rows, degrading to a full
`Seq Scan` at a million — while the cache it protects holds 512 statements per
connection and a miss costs **21 µs, once**. Trading a 66-fold regression for
21 µs is not a trade. Plain `col = $1` remains fully indexable as a parameter;
the `IS NULL OR` construction specifically is what breaks.

### 7.4 The cursor

Carries a format version, the exact sort-key values of the last row
**returned**, and a fingerprint of the query shape: **entity name**, sort spec
and filter set. Encoded as base64url of JSON.

The entity belongs in the fingerprint. Revision 1 omitted it, so two entities
sharing a sort and filter shape — the very shape used as this document's example
— produced interchangeable cursors.

The fingerprint is computed over a **canonical form**: filters sorted by name,
values in declaration order. Iterating a Go map to build it would make a client
reject its own cursor nondeterministically.

**Cursors are readable.** base64url of JSON is not encryption, and the values
land in access logs, `Referer` headers and browser history. Phase 3 must ensure
no sort or filter column carries data the caller may not read.

**No HMAC in phase 1.** It is defence in depth, not a vulnerability fix: a
forged cursor grants no unauthorised access, since authorisation filters are
applied server-side from the request and never from the cursor. It only lands a
caller at an arbitrary position within data they may already read. It arrives in
1.5 together with its key storage, key identifier and rotation window — none of
which revision 1 specified, while requiring the server to fail closed on a
missing key.

Decoding is defensive and **type-checks every value against the IR's sort-key
types before binding**. A well-formed, correctly-signed cursor carrying a string
where a timestamp belongs must be a 400, not a pgx error surfacing as a 500. Any
decode failure is 400: never a fallthrough to a looser query, never a silent
default to the first page.

### 7.5 Timestamps

`timestamptz` stores microseconds; `time.Time` carries nanoseconds. **pgtype
truncates client-side** when binding — not server-side, so it is invisible to
server-side debugging and applies in every exec mode. Postgres, meanwhile,
*rounds* a text literal. A cursor timestamp that did not come from Postgres
therefore does not compare the way the application intends, and rows at the page
boundary are skipped or repeated.

Three rules follow:

1. **Build the cursor from the `time.Time` pgx returned when scanning the row.**
   It is already microsecond-quantised and round-trips exactly.
2. **Normalise to UTC before serialising.** pgx scans `timestamptz` into
   `time.Local` by default (`ScanLocation` is nil), so the same instant
   serialises to different bytes depending on the host's `TZ` — cursors would
   not survive a multi-instance deployment. Set `ScanLocation: time.UTC`.
3. **Never compare or order cursor strings lexically.** `time.Time`'s JSON
   encoding strips trailing zeros, so `"…30Z"` sorts after `"…30.5Z"`.

Note also that `time.Time.MarshalJSON` errors for years outside [0, 9999] while
`timestamptz` spans far wider. A row with such a timestamp fails cursor
encoding; the validator cannot prevent it, so the store returns a 500 with a
clear log line rather than a panic.

### 7.6 The `limit+1` peek

Fetch `limit+1` rows to detect a next page. **Build the next cursor from the
last *returned* row, not the peeked row**, and trim before serialising. The
inverse is an off-by-one that skips or duplicates exactly one row per page — a
bug that survives a demo and dies in production.

### 7.7 Accepted behaviour

Under `READ COMMITTED` each page runs against a different snapshot, so
concurrent inserts and deletes can produce a phantom row across a page boundary.
Expected, documented, and distinct from the skip and duplicate bugs above.

---

## 8. Testing

Test-driven. The failing test comes first.

- **Parser and validator** — table-driven over fixture YAML, error cases as
  thoroughly as success cases. **Diagnostic text is part of the public contract
  and tests assert on it**, including caret placement for a non-ASCII
  identifier, and the tab-rejection diagnostic.
- **Templates** — golden files under `testdata/<case>/` with the `-update` flag
  convention, so regeneration is reviewed as a diff.
- **Generated code compiles.** Golden output is written to a temp module and
  built with `go build`. Revision 1 promised a `go/types` tier with "no
  subprocess"; generated code imports pgx, and resolving that requires
  `go/packages`, which shells out to `go list`. The claim was false, so the tier
  is merged into the build tier and the cost accepted.
- **Determinism** — twenty runs, byte equality, under `-race`.
- **Store, against a real Postgres**, using the DDL this phase generates:
  - full pagination yields every row exactly once;
  - ties on the first sort key paginate correctly;
  - rows inserted mid-pagination never skip or duplicate a row that existed when
    pagination began;
  - a sub-microsecond timestamp round-trips without boundary loss;
  - cursors are stable across `TZ=UTC` and `TZ=Europe/Paris`;
  - a forged, truncated, foreign-entity or wrong-typed cursor yields 400, never
    a panic — with a fuzz test behind that guarantee;
  - `DisallowUnknownFields` rejects an attempt to set a `readonly` field.

### 8.1 The performance assertion, done properly

Revision 1 asserted that `EXPLAIN` shows an Index Scan and no Sort node. **Both
catastrophic plans above satisfy that assertion.** The OR-chain reports
`Index Only Scan` with no `Sort`; a direction mismatch produces `Incremental
Sort`, whose node name a naive check misses; and a correct and an incorrect
`Index Cond` can print byte-identical lines two orders of magnitude apart.

The assertion is therefore on **invariance, not plan shape**:

- `Buffers` for page 10 000 is within a small constant factor of page 1.
- `Rows Removed by Filter` is approximately zero.

A test that cannot fail on the bug it exists to catch is worse than no test,
because it is believed.

---

## 9. Open questions

1. **Pluralisation.** `table:` defaults to name + `s`. Naive, predictable, no
   dictionary; an author who disagrees writes `table:` explicitly.
2. **`json` fields** are neither sortable nor filterable in phase 1.
3. **Composite primary keys** are deferred; exactly one `pk` per entity.
4. **`max` on `string`** emits both `varchar(n)` and a Go validation check. The
   two can disagree if the migration is edited by hand; phase 2's diffing is
   what resolves that properly.

---

## 10. Build order

1. `Pos`, `At[T]`, `Diagnostic`, rendering, tab rejection.
2. Parser: YAML → AST → IR with positions.
3. Validator: §3.1, §3.3, §3.4, §5.6.
4. DDL emitter: `CREATE TABLE`, constraints, indexes from §7.2.
5. Template engine, formatting, determinism, staging, lock.
6. Hooks interfaces and no-op implementations.
7. Model and input types (§6.5), store, cursor encoding, keyset predicate.
8. Handlers, error envelope, router, resource bounds.
9. `lapigo new`, `lapigo gen`.

The dependency order is real and revision 1 got it wrong by claiming steps 5–7
could run in parallel: the store invokes hooks inside its transaction, so hooks
precede the store, and handlers depend on the store's types and cursor
encoding. **7 depends on 6; 8 depends on 7.** Only 4 is genuinely independent of
5–8 once 3 is in place.

---

## 11. What must be true before phase 1 is done

- `lapigo new demo && lapigo gen && psql -f migrations/0001_init.sql && go run ./cmd/api`
  serves a working CRUD API.
- Paginating a 500 000 row table reads a constant number of buffers per page,
  independent of depth, proven by the §8.1 assertion.
- No generated SQL contains an interpolated identifier or value.
- Every error response is the §6.7 envelope, and no `500` body contains a driver
  message.

---

## 12. Changes from revision 1

Recorded so the reasoning is not rediscovered.

| Change | Reason |
|---|---|
| DDL moved into phase 1 | Revision 1 generated a store for tables nothing created. The deliverable could not be run or tested. |
| `index: true` removed | A user-asserted claim about a database lapigo neither creates nor inspects. The generator derives indexes instead. |
| Row-value comparison replaces the OR-chain | Measured 4 buffers / 0.05 ms against 5 076 buffers / 50.5 ms. |
| Mixed-direction sorts deferred to 1.5 | They cannot use a row-value comparison; the alternatives need benchmarking. |
| `($1 IS NULL OR col = $1)` removed | Forces a `Filter`, then a `Seq Scan` at scale. The pgx statement-cache justification was worth 21 µs, once. |
| `EXPLAIN` assertion rewritten | The original passed on both catastrophic plans. |
| Direction-specific indexes dropped | A plain ascending index serves both directions of a uniform sort at identical cost. |
| Hook signature takes `pgx.Tx` | Revision 1 required related writes in the transaction while giving hooks no way to reach it. |
| Input projection, update semantics, error envelope, resource bounds specified | Six load-bearing designs an implementer would otherwise have had to invent. |
| `Field`, `Filter`, `Relation`, `Endpoint` defined; sort keys hold `*Field` | The IR was not implementable, and would have forced lookups inside templates. |
| `Imports` moved from `Entity` to the output file | An entity spans four packages with different needs. |
| `FormatOnly: true` dropped | Milliseconds of build time bought with a permanent correctness risk and no correction pass. |
| Tabs rejected in the schema | goccy under-counts tab columns; the test revision 1 promised would have failed against the real library. |
| Cursor: entity added to the fingerprint, canonical form specified, UTC normalisation, no lexical comparison | Cross-entity replay; nondeterministic fingerprints; cursors unstable across hosts because pgx scans into `time.Local`. |
| HMAC deferred to 1.5 | Key storage, key identifier and rotation were unspecified while the server was required to fail closed without a key. |
| Relation expansion deferred to 1.5 | Loading strategy was unspecified, and N+1 avoidance is the project's stated core value. |
| Staging directory dot-prefixed; atomicity claim withdrawn | A plain sibling directory is compiled by `go build ./...`. Two renames are not atomic; the recovery path is stated instead. |
| `go/types` test tier merged into `go build` | "No subprocess" was false once generated code imports pgx. |
| Build order corrected | Hooks precede the store, which precedes the handlers. |
