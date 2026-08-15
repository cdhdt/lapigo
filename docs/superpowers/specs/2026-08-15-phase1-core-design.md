# Phase 1 — Core generator design

Status: **proposed**, awaiting approval
Date: 2026-08-15

Phase 1 delivers the skeleton every later phase builds on: the schema format,
the parser, the intermediate representation, the diagnostic system, the template
engine, and generated CRUD endpoints with cursor pagination, filtering and
sorting.

Nothing here ships to production alone. It ships a generator that turns a
`lapigo.yaml` into a package of Go that compiles, and a list endpoint that
paginates correctly under concurrent writes.

---

## 1. Scope

**In scope**

- `lapigo.yaml` schema format: entities, fields, scalar types, enums,
  `belongsTo` relations, sort specs, filter whitelists, endpoint selection.
- Parsing to an AST with source positions preserved.
- Resolution to a validated intermediate representation (IR).
- Compiler-grade diagnostics: file, line, column, caret span, actionable hint,
  all errors reported at once.
- Template rendering to `internal/gen/`, formatting, deterministic output,
  atomic writes, hand-edit detection.
- Generated code: models, store (pgx), HTTP handlers, router, hooks interfaces.
- CRUD endpoints: `list`, `get`, `create`, `update`, `delete`.
- Cursor pagination with mixed-direction sorts, whitelisted filters.
- `lapigo new` and `lapigo gen` commands.

**Out of scope, deferred to later phases**

- Migrations and schema diffing (phase 2). Phase 1 emits the `CREATE INDEX`
  statements a sort requires as *documentation output*, not as an applied
  migration.
- Authentication and permissions (phase 3).
- OpenAPI generation (phase 4).
- Redis caching (phase 5).
- Many-to-many relations, nested writes, file uploads, realtime, admin UI.

---

## 2. Architecture

### 2.1 Pipeline

```
lapigo.yaml
   │
   ├─ 1. Load       read file(s)
   ├─ 2. Parse      YAML → goccy AST, positions preserved
   ├─ 3. Resolve    AST → IR, defaults expanded, relations resolved
   ├─ 4. Validate   on the IR, accumulating diagnostics
   ├─ 5. Plan       compute the set of output files
   ├─ 6. Render     IR + templates → map[string][]byte, in memory
   ├─ 7. Format     imports.Process per file
   └─ 8. Write      staging directory, atomic rename, manifest
```

Steps 1–7 touch no disk beyond reading the schema. `Generate(schema) (map[string][]byte, error)`
is a pure function of its input. This is not an implementation detail: it is what
makes golden tests, determinism tests and type-checking fast, parallel and free
of filesystem races, and it guarantees that a single template failure leaves the
output tree untouched.

### 2.2 The IR boundary

**Templates consume the IR and only the IR.** They never see YAML, never see the
AST, never see a `map[string]any`.

A new *input* (introspecting an existing Postgres schema) produces the same IR
and touches no template. A new *output* (a TypeScript client, GraphQL) consumes
the same IR and touches no parser. Code that lets the YAML reach a template
directly is rejected in review.

```go
type Schema struct {
    Entities []Entity          // sorted by name — determinism
}

type Entity struct {
    Name     string            // as written in YAML
    GoName   string            // validated Go identifier
    Table    string
    Fields   []Field           // declaration order preserved
    PK       *Field
    Sort     []SortKey         // last element is a unique tiebreaker
    Filters  []Filter          // whitelist
    Relations []Relation
    Endpoints []Endpoint
    Imports  []string          // computed, see §5.2
}

type SortKey struct {
    Field At[string]
    Desc  bool
}
```

`Imports` on the IR is load-bearing. See §5.2.

### 2.3 Dependencies

Generator: `github.com/goccy/go-yaml`, `golang.org/x/tools/imports`, stdlib.

Generated code: stdlib and `github.com/jackc/pgx/v5`. Nothing else, ever,
without an issue and a maintainer's agreement.

`gopkg.in/yaml.v3` is **archived and unmaintained** as of 2026 and must not be
used. `go.yaml.in/yaml/v3` is its maintained continuation but exposes no
structured position information on decode errors, which makes it unfit for the
diagnostics this spec requires.

---

## 3. Schema format

```yaml
entities:
  article:
    table: articles                 # optional, defaults to pluralised name
    fields:
      id:         { type: uuid, pk: true }
      title:      { type: string, required: true, max: 200 }
      body:       { type: text }
      status:     { type: enum, values: [draft, published], required: true }
      views:      { type: int, index: true }
      author:     { type: belongsTo, target: user }
      created_at: { type: timestamp, required: true, default: now }
    sort: [-created_at, id]         # '-' prefix = DESC; last key must be unique
    filters: [status, author]       # whitelist; nothing else is filterable
    endpoints: [list, get, create, update, delete]
```

Scalar types for phase 1: `uuid`, `string`, `text`, `int`, `bigint`, `float`,
`decimal`, `bool`, `timestamp`, `date`, `json`, `enum`.

`decimal` maps to `pgtype.Numeric`, never `float64`. Binary floating point can
reorder two values that are distinct in `NUMERIC`, which silently corrupts a
cursor built on that column.

### 3.1 Sort constraints — enforced by the validator

1. **The last sort key must be unique.** Without a unique tiebreaker a keyset
   scan skips or duplicates rows whenever two records share a sort value. The
   validator resolves uniqueness from `pk: true` or `unique: true`.
2. **No sort key may be nullable.** SQL comparison against `NULL` yields
   unknown, so a row with a `NULL` sort value is silently dropped from every
   page after the seek predicate applies — and a cursor holding a `NULL` loses
   every subsequent row. The failure mode is invisible row loss in production,
   which is strictly worse than an ergonomic complaint at build time. A user who
   needs this declares a `COALESCE` expression as the sort key instead; the
   generator then treats it as a plain non-null expression. Not in phase 1.
3. **Prefer immutable sort keys.** Sorting on a mutable column (`updated_at`)
   means a row can move relative to a live cursor and be skipped or repeated.
   This is inherent to keyset pagination, not a bug. The validator emits a
   *warning*, not an error, and the documentation states the tradeoff.

### 3.2 Filters

Filters are a whitelist. A request may select *which* whitelisted column to
filter on; it may never *name* a column. Identifiers in SQL come from the IR,
never from a request parameter. This is the single rule that makes injection
through sort and filter parameters structurally impossible rather than a matter
of escaping.

---

## 4. Diagnostics

### 4.1 Target output

```
lapigo.yaml:14:5: sort field "views" is not indexed
   14 |     sort: [-views, -created_at]
      |            ^^^^^^
   add `index: true` to the `views` field, or remove it from the sort
```

### 4.2 Carrying positions from YAML to the IR

Positions are captured during the AST walk that builds the IR — the walk visits
every node anyway, so recording a position is a zero-cost side effect.

Two mechanisms, used together:

- **`At[T]`** — a generic wrapper applied *selectively* to the leaves validation
  actually blames: identifiers, enum values, individual sort keys.

  ```go
  type Pos struct{ Line, Column int } // 1-based, column counted in RUNES

  type At[T any] struct {
      Value T
      Pos   Pos
  }
  ```

  A `Pos` field on every IR node would pollute every struct, break `cmp.Diff` in
  tests, and force a position onto synthetic and defaulted values that have
  none. `At[T]` puts the cost exactly where the benefit is, and stays type-safe.

- **`PosTable`** — `map[string]Pos` keyed by the path format goccy's
  `Node.GetPath()` already produces (`entities.article.sort[0]`), as the escape
  hatch for diagnostics that blame a whole subtree rather than a leaf.

The IR package defines its own `Pos` and does not import the YAML parser.

### 4.3 One diagnostic type for syntax and semantic errors

goccy exposes `*yaml.SyntaxError` carrying a token with a position. That is
converted into the same `Diagnostic` struct the validators produce, so parse
errors and semantic errors interleave and sort together by position. A file with
one syntax typo and one indexing violation prints both, in source order, in one
format.

goccy's own `yaml.FormatError` renders a caret snippet already, but it bypasses
our tab and UTF-8 handling and cannot interleave with semantic diagnostics.
Extract `(Pos, Message)` from the error and re-render through the shared path.

### 4.4 Accumulate, sort, print once

Validators append to a shared `Diagnostics` accumulator and keep going. Nothing
stops at the first error. Rendering happens once, at the top level, sorted by
position. This is the `go/scanner.ErrorList` shape, and it is the difference
between fixing ten schema errors in one pass and fixing them one build at a
time.

### 4.5 Two rendering traps that must be tested

- **Rune versus byte columns.** YAML parsers count columns in runes; Go string
  slicing is byte-indexed. Mixing them misplaces the caret on any line
  containing a multi-byte character — an accented identifier is enough. Column
  arithmetic is in runes throughout, converted only at render time.
- **Tabs.** The caret line must align with the source line regardless of tab
  width. Expand tabs to spaces for display and keep a rune-index → display-column
  map so a caret computed from a rune column still lands under the right
  character.

Both get explicit test cases with non-ASCII identifiers and tab-indented YAML.

---

## 5. Generation

### 5.1 Templates

`text/template`, split across files, embedded with `embed.FS` and `ParseFS`.
Shared blocks (`DO NOT EDIT` header, package clause) live in one file and are
pulled in with `{{template "header" .}}`.

- **`Option("missingkey=error")` is mandatory.** Without it a typo in a field
  name renders `<no value>` silently instead of failing.
- **Templates are nearly logic-free.** Type mapping, casing and naming decisions
  live in Go, where they are unit-testable and produce real stack traces. Nested
  `{{if eq .Type "string"}}` chains in a template are rejected in review.
- **Never `range` a Go map inside a template.** The IR exposes pre-sorted
  slices; see §5.3.
- **`strconv.Quote` for every user-supplied string** reaching a Go string
  literal position.

### 5.2 Formatting — the IR carries its imports

The obvious choice is `golang.org/x/tools/imports.Process`, which resolves the
import block by scanning the module dependency graph for the symbols used. It is
also slow, per file, for exactly that reason.

**The IR already knows which packages each file needs** — it knows every type
referenced. So the IR computes the import set, the template renders an explicit
import block, and `imports.Process` runs with `FormatOnly: true`, degrading it to
gofmt's cost with a consistent API. Paying to guess what we already know is the
avoidable mistake here.

When rendered text fails to parse, that is a template bug, never a user data
problem. The error must present the *unformatted* output with line numbers, so
the reported `file:line:col` points at something the developer can actually
read. Generation then fails hard: no unformatted or partial Go is ever written.

### 5.3 Determinism

Same schema in, byte-identical files out.

| Source | Mitigation |
|---|---|
| `range` over a Go map | The IR exposes sorted slices. Maps never reach a template. |
| Wall-clock in headers | **No timestamp in generated output.** A "generated at" comment produces a diff on every CI run. |
| Absolute paths | Paths are relative to the module root. |
| Generator version string | Pinned at build time via `-ldflags -X`, never computed per run. |
| Worker pool completion order | One owning goroutine per output file; results collected by index, merged in index order. |
| Input file discovery order | Schema file lists are explicitly sorted, never left to filesystem enumeration order. |

Tested by generating twenty times in-process and asserting byte equality, under
`-race` to catch the shared-mutable-state class directly.

### 5.4 Writing

1. Render every file to memory. A failure anywhere aborts before touching disk.
2. Write into a staging directory that is a **sibling** of the output directory —
   `os.Rename` is atomic only within a filesystem.
3. Rename the old output directory aside, rename staging into place, remove the
   old one. A crash leaves either the old tree or the new tree, never a torn mix.
4. Sweep orphaned staging directories on the next run.

`.lapigo.lock` records a SHA-256 per generated file plus the generator version.
Before regenerating, recompute checksums of what is on disk; any mismatch means
a generated file was hand-edited. **Stop and report it.** A hand-edit is almost
always a signal that an extension point is missing, and the safe failure mode is
to tell a human, not to delete their work. `--force` overrides.

Every generated file opens with the canonical marker, whose exact form matters
because tooling matches it by regexp (`^// Code generated .* DO NOT EDIT\.$`):

```go
// Code generated by lapigo. DO NOT EDIT.
```

### 5.5 Names: reject, never mangle

Schema names map to Go identifiers. Collisions and invalid identifiers are
**validation errors with a position**, not silently mangled.

Mangling makes the generated Go name a function of the generator version: if v1
resolves a collision to `UserType2` and v2 to `UserType_`, every consumer of the
generated API breaks silently on upgrade. It also hides schema bugs — two fields
colliding after export-casing are nearly always a copy-paste mistake, not a
deliberate pair.

Checks, all across the whole package rather than per file:

- Go keywords and predeclared identifiers.
- Leading digits, empty results, non-identifier runes.
- Case collisions after export (`user_id` and `userId` both yield `UserID`).
- A generated field colliding with a generated method (`validate` → `Validate`).
- Collision with an identifier the generated package already exports.

---

## 6. The generated project

### 6.1 Two commands, two guarantees

```
lapigo new myapp     # once: go.mod, cmd/api/main.go, lapigo.yaml
lapigo gen           # repeatedly: writes ONLY internal/gen/
```

```
myapp/
├── lapigo.yaml               # yours
├── go.mod                    # yours
├── cmd/api/main.go           # yours (scaffolded once, never touched again)
├── internal/
│   ├── gen/                  # lapigo, rewritten in full
│   │   ├── model/
│   │   ├── store/
│   │   ├── httpapi/
│   │   └── hooks/
│   └── app/                  # yours: hook implementations, custom routes
└── .lapigo.lock              # lapigo
```

### 6.2 Four escape hatches

A generator dies the day a user steps outside its frame and finds nowhere to go.

1. **Hooks** — act before or after an operation.
2. **Custom routes** — the generated router returns a `*http.ServeMux` to add to.
3. **Store primitives** — the generated store exposes its query builders, so
   custom queries reuse the keyset machinery instead of reimplementing it.
4. **Opt-out** — `endpoints: [list, get]` simply does not generate `create`.
   Write it yourself, alongside, without fighting the tool.

### 6.3 Hooks: the embedded no-op is what makes them evolvable

```go
// generated
type ArticleHooks interface {
    BeforeCreate(context.Context, *ArticleCreateInput) error
    AfterCreate(context.Context, *Article) error
    AfterCreateCommitted(context.Context, *Article)
    // ...
}

type NoopArticleHooks struct{}
func (NoopArticleHooks) BeforeCreate(context.Context, *ArticleCreateInput) error { return nil }
// ...
```

```go
// yours
type ArticleHooks struct{ gen.NoopArticleHooks }

func (h ArticleHooks) BeforeCreate(ctx context.Context, in *gen.ArticleCreateInput) error {
    in.Slug = slugify(in.Title)
    return nil
}
```

Embedding the no-op means a user implements only what they need, **and adding a
hook method in a future lapigo release does not break their build.** Without it,
every new hook is a breaking change for every user.

Registration is explicit and typed, in `main.go`. No reflection, no discovery by
file-naming convention.

### 6.4 Transaction boundaries

A hook that fails must roll the write back, so hooks run inside the same
transaction. But a hook that sends an email must not fire for a transaction that
was rolled back. Hence two families:

| Hook | Context | Can abort | For |
|---|---|---|---|
| `BeforeCreate` / `AfterCreate` | inside the transaction | yes | validation, derived fields, related writes |
| `AfterCreateCommitted` | after commit | no | emails, webhooks, cache invalidation |

Phase 5 cache invalidation hangs off `AfterCreateCommitted`. Invalidating before
commit is a race that re-caches the pre-write state.

---

## 7. Cursor pagination

### 7.1 Always the OR-chain, never the row-value form

Postgres row-value comparison `(a, b) < (x, y)` expands to a lexicographic
comparison using the *same* operator for every column. It is therefore only
correct when all sort columns run in the same direction — and
`created_at DESC, id ASC` is an ordinary sort spec.

The generator emits the expanded OR-chain, always:

```sql
WHERE k1 OP1 $1
   OR (k1 = $1 AND k2 OP2 $2)
   OR (k1 = $1 AND k2 = $2 AND k3 OP3 $3)
ORDER BY ...
LIMIT $n
```

It is strictly more general, its index requirements are identical to the
row-value form's, and standardising on one shape removes a special case from the
templates at no cost.

### 7.2 Index requirements

An index serves a keyset seek only if its column list, order **and per-column
direction** match the `ORDER BY` exactly. A btree can be scanned backwards, but
that reverses *every* column at once: `(created_at ASC, id ASC)` serves
`ORDER BY created_at DESC, id DESC` and does **not** serve
`created_at DESC, id ASC`.

With filters, column order in the composite index is fixed: **equality filter
columns first, then the sort columns in their exact order and direction.**

```sql
-- WHERE status = $1 ORDER BY created_at DESC, id DESC
CREATE INDEX article_status_created_id_idx
  ON articles (status, created_at DESC, id DESC);
```

A btree uses any number of leading equality conditions as its prefix, but only
one range condition as the seek boundary; everything after it degrades to a
filter. Getting this wrong costs no correctness and all of the performance —
which is the entire point of keyset pagination. The generator therefore emits
the required `CREATE INDEX` for every declared sort/filter combination, and
phase 2 turns those into migrations.

### 7.3 The cursor

Carries: a format version byte, the exact values of every sort column for the
last row **returned**, and a fingerprint of the query shape (sort spec plus
filter set). Encoded as base64url of JSON — debuggable, trivially versioned, and
cursor size is irrelevant next to any URL budget.

The fingerprint exists so a cursor minted for `sort=-created_at` cannot be
replayed against `sort=+title` and produce a nonsensical seek.

**Cursors are HMAC-tagged**, with two keys accepted during rotation and the key
generated by `lapigo new`. To be accurate about what this buys: it is defence in
depth, **not** the fix for a vulnerability. A forged cursor grants no
unauthorised access — authorisation filters are applied server-side from the
request, never from the cursor — it only lands the caller at an arbitrary
position within data they may already read. What the tag actually provides is a
single rejection path for malformed and tampered input, and a fingerprint check
that means something against a motivated caller. The spec should not oversell it.

Any decode failure is **400**. Never fall through to a looser query, never
silently default to the first page (that masks client bugs), never let a
malformed value reach a code path that could become an unbounded scan.

### 7.4 Timestamp precision — the sharpest edge

Postgres `timestamptz` stores microseconds. Go `time.Time` carries nanoseconds.
A cursor timestamp that did not come from Postgres is truncated server-side on
binding, so the comparison the application intends is not the comparison
Postgres performs, and rows at the page boundary are skipped or repeated.

**The cursor is always built from the `time.Time` that pgx returned when
scanning the row**, never from an application-generated value. The scanned value
is already microsecond-quantised and round-trips exactly. Serialisation uses
full precision (RFC3339Nano, which is `encoding/json`'s default for `time.Time`)
— never `Unix()`, `UnixMilli()`, or plain RFC3339.

### 7.5 SQL text must be stable per endpoint

pgx caches prepared statements keyed by exact SQL text. Building different query
strings depending on which optional filters are present defeats that cache and
adds prepare latency to every request. Optional filters are expressed as
`($1::type IS NULL OR col = $1)`; the statement shape is constant per endpoint.

### 7.6 The `limit+1` peek

To report whether a next page exists, fetch `limit+1` rows. **Build the next
cursor from the last *returned* row, not the peeked row**, and trim before
serialising. Getting this backwards is an off-by-one that skips or duplicates
exactly one row per page — the kind of bug that survives a demo and dies in
production.

`limit` is clamped server-side. Zero, negative and enormous values are rejected
before they reach a query.

### 7.7 Known and accepted behaviour

Under `READ COMMITTED`, each page runs against a different snapshot, so
concurrent inserts and deletes can produce a phantom row across a page boundary.
This is expected, documented, and distinct from the skip/duplicate bugs above.

---

## 8. Testing

Test-driven. The failing test comes first.

- **Parser and validator** — table-driven over fixture YAML, covering error
  cases as thoroughly as success cases. **Diagnostic text is part of the public
  contract; tests assert on it**, including caret placement for a non-ASCII
  identifier and for tab-indented input.
- **Templates** — golden files under `testdata/<case>/`, with the `-update` flag
  convention so regeneration is reviewed as a diff rather than hand-edited.
- **Generated code must type-check.** Every golden case is parsed and run
  through `go/types` in the fast unit tier — milliseconds, no subprocess. A
  representative subset additionally gets a real `go build` in an integration
  tier, which is the only way to catch build-tag and toolchain-level divergence.
  Not every case: subprocess cost multiplies badly as the corpus grows.
- **Determinism** — generate twenty times, assert byte equality, run under
  `-race`.
- **Store, against a real Postgres** — the keyset correctness tests are the
  centre of gravity of this phase:
  - full pagination of a table yields every row exactly once;
  - ties on the first sort column paginate correctly (the tiebreaker works);
  - mixed-direction sorts paginate correctly;
  - **rows inserted mid-pagination never cause a skip or a duplicate** among
    rows that existed when pagination began;
  - a timestamp with sub-microsecond input round-trips without boundary loss;
  - a forged, truncated, or foreign-fingerprint cursor yields 400 and never a
    panic;
  - `EXPLAIN` confirms an Index Scan, not a Seq Scan or a Sort node, for each
    generated sort/filter combination.

A test is not passing until its output has been read.

---

## 9. Open questions

1. **Plural forms.** `table:` defaults to a pluralised entity name. English
   pluralisation is irregular and a generator that guesses wrong is annoying
   forever. Proposal: naive `+s`, and require an explicit `table:` whenever the
   author disagrees. Cheap, predictable, no dictionary.
2. **`json` field type and cursors.** A `json` column must not be sortable or
   filterable in phase 1. The validator rejects it.
3. **Composite primary keys.** Deferred. Phase 1 requires exactly one `pk`.
4. **HMAC key absent.** If `lapigo new` generated a key and it is later missing
   from configuration, the server must refuse to start rather than silently
   issue unsigned cursors. Fail closed.

---

## 10. Build order

1. `Pos`, `At[T]`, `Diagnostic`, `Diagnostics`, rendering. Everything else
   reports errors through this, so it comes first.
2. Parser: YAML → AST → IR, positions threaded.
3. Validator: the rules in §3.1, §3.2 and §5.5.
4. Template engine, formatting, determinism, writing, manifest.
5. Model and store templates, cursor encoding, the OR-chain builder.
6. HTTP handler and router templates.
7. Hooks interfaces and no-op implementations.
8. `lapigo new` and `lapigo gen`.

Steps 1–3 are strictly sequential. Steps 5–7 can proceed in parallel once 4 is
in place.
