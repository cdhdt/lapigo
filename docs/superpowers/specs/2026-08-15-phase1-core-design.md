# Phase 1 — Core generator design

Status: **accepted**, revision 2
Date: 2026-08-15
Amended: through 2026-09-11 — every amendment is listed in §13

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
  sorts and filters require, plus any declared composite indexes (§3.5).
- Templates → `internal/gen/`, formatted, deterministic, written through a
  staging directory with a stated recovery path (§5.4 — *not* atomically; the
  claim was withdrawn, see §12).
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
   ├─ 8. Format      imports.Process, FormatOnly (§5.2)
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

**Step 6 is what makes step 8 hermetic.** Plan computes each output file's
import set from the IR — it already did in revision 2, and §2.2 already says
imports are a property of a file — and step 8 formats with `FormatOnly: true`,
so goimports is never asked to resolve anything. Import *resolution* scans the
module cache and shells out to `go list`: filesystem access, a subprocess, and
an ambient dependency, inside the function this section calls hermetic. It is
also non-deterministic in practice, not only in principle — the measurements are
in §5.2. The import set is corrected by the Go compiler, not by goimports
(§5.2, §8).

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
    Sort      SortSpec           // never empty; [-PK] when `sort:` was omitted (§3.3)
    Filters   []Filter           // declaration order; SortedFilters() returns
                                  // a canonical by-name copy for §7.4
    Indexes   []Index            // declared composite indexes, declaration
                                  // order (§3.5); the derived set of §7.2 is
                                  // NOT stored here — the DDL emitter computes it
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
    EnumValues []EnumValue
    Default    *DefaultValue
}

// One member of an enum field's `values:` list, paired with the exported Go
// identifier the generator emits for it (e.g. "InProgress" for "in_progress"),
// so §5.6's collision check has a real identifier to compare (§13, 2026-09-09).
type EnumValue struct {
    Name   source.At[string]      // as written
    GoName string
}

// Derived, never stored. See below.
func (f *Field) GoType() string       // the MODEL's type: *string when nullable
func (f *Field) ValueGoType() string  // nullability stripped: the T of Optional[T]
func (f *Field) PgType() string

type SortSpec struct {
    Keys []SortKey               // last key resolves to a unique field
    Desc bool                    // ONE direction for the whole spec (§7.1)
}

type SortKey struct {
    Field *Field                 // RESOLVED pointer, not a name
    Span  source.Span            // the entry in `sort:`, sign included; zero
                                  // for the synthesized default (§3.3) — the
                                  // parser wrote nothing to blame
}

type Filter struct {
    Field *Field                 // resolved
    Op    FilterOp               // Eq only in phase 1
    Span  source.Span            // the entry in `filters:`
}

// Derived, never stored.
func (op FilterOp) SQL() string  // the SQL operator, e.g. "=" for FilterOpEq

type Relation struct {
    Name       string            // "author"
    NameSpan   source.Span
    GoName     string            // "Author"
    Target     *Entity           // resolved
    TargetSpan source.Span       // the `target:` value; always valid (see below)
    Column     string            // "author_id"
    GoType     string            // from the target's PK
    Nullable   bool
    OnDelete   string            // "RESTRICT" default
}

type Endpoint struct {
    Kind EndpointKind            // List, Get, Create, Update, Delete
}

// Derived, never stored. The wildcard in the single-resource path is named
// after e.PK.Column, not hardcoded to "{id}" -- an entity whose PK is
// "slug" gets "/things/{slug}".
func (e *Entity) Path(kind EndpointKind) string // e.g. "/articles", "/articles/{id}"

// Derived, never stored.
func (k EndpointKind) Method() string  // "GET"/"POST"/"PATCH"/"DELETE", for the
                                         // Go 1.22 "METHOD /path" pattern
func (e *Entity) HasList() bool
func (e *Entity) HasGet() bool
func (e *Entity) HasCreate() bool
func (e *Entity) HasUpdate() bool
func (e *Entity) HasDelete() bool
```

**Every `Relation` that exists carries a valid `TargetSpan`.** A relation is
appended only once its `target:` has been read *and* resolved to a real entity:
an omitted, empty, or unresolvable target reports a diagnostic and returns
before the append, so no `Relation` is built at all. There is therefore no such
thing as a relation whose target was never written, and a diagnostic blaming a
*missing* `target:` cannot be expressed through this type — it must be reported
by the parser, at the relation's own name, before any `Relation` exists.

This is an invariant, so `Schema.Freeze` must check it rather than this
paragraph asserting it. An earlier revision of this spec claimed the opposite —
that `TargetSpan` was the zero `Span` when `target:` was omitted — which an
adversarial review disproved by enumerating all four target forms: only the
resolved one yields a `Relation`, and its span is always valid.

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

**`GoType` answers for the model; `ValueGoType` answers for the input types.**
`GoType` returns the pointer form for a nullable field, which is right for a
struct that is marshalled (§3.2) and wrong for one that is decoded, where §6.5
uses `Optional[T]` instead. Templates may not compute the difference themselves
(§5.1), so it is a second accessor rather than a `strings.TrimPrefix` in a
template — the same omission class as the accessors §13 records for issue #25.

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

Generator: `github.com/goccy/go-yaml`, `golang.org/x/tools/imports`, stdlib, plus
`github.com/jackc/pgx/v5` in tests only (§8's DDL apply tier).
Generated code: stdlib and `github.com/jackc/pgx/v5`. Nothing else.

**The pgx version a generated project pins is a build-time constant of the
lapigo binary**, set with `-ldflags -X` exactly as the generator version is
(§5.3), never read from the generator's own `go.mod` at run time and never
resolved from the network. `lapigo new` writes it into `go.mod` together with
pgx's indirect requirements and an embedded `go.sum` (§6.1), because §11
asserts that `lapigo new → lapigo gen → psql → go run` works with no
`go mod tidy` and no network step.

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
      created_at: { type: timestamp, required: true, default: now, readonly: true }
      version:    { type: int, required: true, version: true }
    sort: [-created_at, -id]           # ONE direction for the whole spec
    filters: [status, author]
    endpoints: [list, get, create, update, delete]
```

### 3.1 Field options — the complete set

| Option | Meaning |
|---|---|
| `type` | Required. See §3.2. |
| `pk` | Primary key. Exactly one per entity in phase 1. |
| `required` | `NOT NULL`. What it means for the input types is §6.5's to say, not this row's — see the note below the table. |
| `unique` | `UNIQUE` constraint. Makes the field eligible as a sort tiebreaker. |
| `max` | Maximum length for `string`. Emits `varchar(n)` **and** a validation check. |
| `values` | Enum members. Emits a `CHECK` constraint. |
| `target` | `belongsTo` target entity. |
| `on_delete` | `restrict` (default), `cascade`, `set_null`. |
| `default` | `now`, `uuid`, or a literal. **Supplied at insert when the request omits the field.** The field stays settable — a client may send it on create and on update (§6.5). |
| `readonly` | Persisted and returned, **never accepted from a request** — not on create, not on update. This, not `default:`, is how a field is made unsettable. |
| `immutable` | Accepted on create, rejected on update. |
| `version` | Optimistic concurrency column. At most one per entity, and constrained by §3.6. Excluded from both input types (§6.5). |

`default:` and `readonly:` are not synonyms, and reading them as synonyms cost
this project a merged defect. Under the earlier wording — "`default:` excludes
the field from create input", combined with §6.5 building the update input by
*subtraction* from the create input — a field carrying a default was in neither
input type, so no client could ever set it, on create or on update. For
`created_at` that happened to be the intent. For
`status: { type: enum, values: [draft, published], default: draft }` it produced
a CRUD API in which the status can never be changed, and it deleted the
commonest field shape in the format: optional, with a fallback. `default:` now
means only what its name says, and the two options compose:
`default: now, readonly: true` is the `created_at` shape, spelled out.

`readonly:` subsumes `immutable:` — a field never accepted from a request is
also never accepted on update — so writing both is legal and redundant.

**No option in this table states an input projection any more.** `required:`
said "and required in create input" and `default:` said "excludes the field
from create input", and each was a second, drifting copy of a rule §6.5 owns.
The first copy is what produced #16; the second contradicted §6.5's own table
within eight lines of it. §6.5 is the single statement of what a client may
send, and this table says only what each option means to the *database*.

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

**Nullable fields use the pointer form (`*string`) in the model.** The reason is
marshalling, not decoding: a model is output-only — a handler fills it from a
scanned row and marshals it — and a nil pointer marshals to the JSON literal
`null`, which is exactly what a column holding NULL must serialise to (§6.9.2
also forbids `omitempty` in a model, so a null field never simply vanishes).

It is **not** true that a pointer distinguishes "absent" from "explicitly null"
on the way in, and an earlier revision of this document asserted that it did,
here and again in §6.5. `encoding/json` sets a pointer to `nil` for the literal
`null`, which is the same state absence leaves it in — `{}` and
`{"body": null}` are indistinguishable after decoding — and `**T` fails
identically, because `null` sets the outer pointer to nil before the inner one
is ever allocated. `DisallowUnknownFields` does not help; it rejects keys the
struct does not have, not values it does. **Input types therefore do not use
pointers.** §6.5 specifies the generated `Optional[T]` that does distinguish the
three states.

`uuid` primary keys are generated in Go with `crypto/rand` (RFC 4122 v4, about
fifteen lines, no dependency), not by a database default, so create returns the
identifier without a round trip and the code stays portable.

### 3.3 Sort constraints, enforced by the validator

**A sort spec is never empty by the time it reaches the validator.** CLAUDE.md
decision 4 states this as a requirement on the whole system, not only on the
validator: "the validator rejects sorts that lack [a unique tiebreaker] — this
must not be possible to express." An entity that omits `sort:` altogether
still needs one, and an entity that writes `sort: []` has asked for one and
been refused, so both are settled by the *parser*, before rule 2 below ever
runs:

- **Omitting `sort:` synthesizes `sort: [-<pk>]`.** The primary key is unique
  by construction (exactly one per entity, §3.1), so it is always a safe,
  unambiguous default tiebreaker — rule 2 is satisfied automatically, with
  `Entity.Sort.Desc` set to `true`. The synthesized `SortKey`'s `Span` is the
  zero `source.Span`: nothing was written in the schema file for a diagnostic
  to ever blame (`source.Bare`'s own contract).
- **Writing `sort: []` explicitly is a parse-time error.** The author asked
  for a keyset scan with no tiebreaker, spelled out, and is told so directly —
  a parse diagnostic blaming the empty `[]` — rather than having it silently
  default out from under them the way an *omitted* `sort:` does. Silently
  defaulting an explicit empty list would treat "I want no keys" and "I didn't
  think about it" as the same request, which they are not.

This is why the numbered rules below can assume `Keys` is non-empty by
construction: an *ir.Entity* with `len(Sort.Keys) == 0` cannot come out of a
clean parse (zero diagnostics).

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

### 3.5 Entity options

The keys an entity may declare beside `fields:`, `sort:`, `filters:` and
`endpoints:`:

| Option | Meaning |
|---|---|
| `table` | The table name. Optional; default is the entity name + `s` (§9.1). Must satisfy the identifier grammar and Postgres's 63-byte identifier limit. |
| `indexes` | Declared composite indexes (below). Optional. |

`indexes:` is the escape hatch §7.2 promises for a filter combination the
derived *N+1* index set does not cover. Each entry is a mapping with exactly
one key, `filters`, naming at least two filters of the same entity:

```yaml
filters: [status, author]
indexes:
  - filters: [status, author]
```

The declared filter columns, in the order written, lead the emitted index;
the entity's sort keys follow (§7.2). The validator enforces:

- every named column must be a declared filter of the same entity — an index
  exists to serve a filter combination, and a column the API cannot filter on
  serves no combination;
- at least two columns — the derived set already indexes every single
  declared filter, so a one-column entry would emit the same index twice;
- no column twice within one entry, and no two entries with the same ordered
  column list — same index, twice the write cost.

The reasoning that removed revision 1's field-level `index: true` does not
apply here: that option was a user-asserted claim about a database lapigo
neither created nor inspected, while `indexes:` is an instruction to the DDL
emitter lapigo itself runs — phase 1 owns the `CREATE TABLE` and the `CREATE
INDEX`, so nothing is asserted about a database that does not yet exist.

### 3.6 The version column, enforced by the validator

`version: true` is the only field option that turns a column into a control
mechanism rather than data (§6.6). Revision 2 constrained it to "at most one per
entity" and nothing else, and every remaining degree of freedom is a way to
generate a store that cannot work. All four rules are enforced by the validator,
with a position on the offending field.

1. **The type is `int` or `bigint`.** Nothing else. Measured against the merged
   parser: `version: { type: text, version: true }` and
   `{ type: json, version: true }` both validate clean today, and step 7 would
   emit `SET version = version + 1` against them.
2. **`required: true` is mandatory.** A nullable version column emits a nullable
   integer, and a row whose version is NULL makes every `version = $n`
   comparison yield unknown — so §6.6's `If-Match` update matches zero rows and
   returns `409` forever, for that row, permanently. This is §3.3 rule 3's
   reasoning ("SQL comparison against NULL yields unknown") applied to a
   different column. This resolves open question 6.
3. **It may not also be the primary key.** Measured:
   `id: { type: uuid, pk: true, version: true }` validates clean today, and step
   7 would emit `SET id = id + 1` on a uuid. Even with an integer key, a primary
   key that renumbers itself on every update is not a primary key.
4. **It is excluded from `CreateInput` and `UpdateInput`** — its own row in
   §6.5's table, not a synthesized flag. Without the exclusion a client can
   `PATCH` its own version and defeat `If-Match` entirely, which inverts the one
   security control §6.5 exists to state.

**Rule 4 is deliberately not implemented by setting `ReadOnly` on the field**,
and this is the trap in the whole decision. `internal/validate/sort.go` exempts
`ReadOnly` fields from §3.3 rule 5's mutable-sort-key warning, so synthesizing
the flag would silently delete that warning for a version column — and a version
column is the worst possible sort key in the format, since its value changes on
every single update. The input projection and the mutability warning ask
different questions and must not share a field.

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

`golang.org/x/tools/imports.Process`, with **`FormatOnly: true`** — gofmt plus
import-block grouping and sorting, and no resolution. The import set comes from
step 6 (§2.1), which already computes it, and §2.2 already makes it a property
of the output file.

**This reverses revision 2's own reversal, and that reversal was wrong on all
three of its premises.** §12 records it as "`FormatOnly: true` dropped —
milliseconds of build time bought with a permanent correctness risk and no
correction pass". Measured against goimports with resolution enabled:

- *It is not milliseconds.* Resolution costs **135–550 ms per file**, because it
  scans the module cache and shells out to `go list`. That is the smallest of
  the three problems, and it is the only one revision 2 weighed.
- *Resolution **is** the correctness risk.* Against a target `go.mod` correctly
  requiring `github.com/jackc/pgx/v5 v5.10.0`, goimports added
  `github.com/jackc/pgx` — the **pre-v5** module, present in that machine's
  cache — and exited 0 with no error. Against an empty module cache it dropped
  the pgx import **entirely** and exited 0. Same schema, same `go.mod`,
  different bytes depending on which modules the developer happens to have
  downloaded: a direct violation of §5.3 that no amount of sorting fixes,
  producing output that imports the wrong module in one case and does not
  compile in the other, silently in both.
- *The correction pass already exists, and it is the compiler.* §8 mandates that
  golden output be written to a temp module and built with `go build`. That tier
  catches an error in the computed set in **both** directions — a missing import
  is `undefined: pgx`, a surplus one is `imported and not used` — which is
  strictly more than goimports offers, since goimports reports neither.

`FormatOnly: true` still runs gofmt and still groups and sorts the import block,
so the formatting contract this section states is unchanged; only the resolution
is gone. With it goes the second half of the problem: revision 2 passed a
`filename` pointing into `internal/gen`, a directory that at render time is
either the tree about to be renamed away (§5.4) or absent on a first run, and
goimports' behaviour against a non-existent directory is undefined. A hermetic
formatter has no filename to be wrong about.

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
| goimports import resolution | Removed. `FormatOnly: true`; the import set comes from step 6. Resolution reads the machine's module cache, so the same schema produced different imports on two machines — measured, §5.2. |
| pgx version in a generated `go.mod` | Pinned at build time with `-ldflags -X`, never resolved per run (§2.3). |

Tested by generating twenty times and asserting byte equality of every file,
under `-race`.

### 5.4 Writing

`Generate`'s map contains Go files under `internal/gen` and nothing else. The
initial migration is **not** in it: it is not Go, it must never reach the
formatter, it does not live under `internal/gen`, and it is not written by the
swap below. Step 9's CLI owns it (§6.1), which keeps this map homogeneous —
every entry is a Go file that is formatted, staged and swapped by the same four
steps, with no per-entry exception to carry through §5.2, §5.3 and §5.5.

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

`.lapigo.lock` records the generator version plus one entry per file lapigo has
written. It is **committed to version control** — a fresh clone must know what
the previous generation produced.

**Every entry carries a kind**, because the lock answers two different questions
and revision 2 conflated them:

| Kind | Files | Checksum records | Compared to disk |
|---|---|---|---|
| `generated` | `internal/gen/**` | the file as written | yes, every run |
| `emitted-once` | `migrations/0001_init.sql` | what lapigo **emitted** at creation | **never** |

*"Was this hand-edited?"* protects a file that is about to be **overwritten**.
It is worth a hard stop precisely because the answer decides whether work is
about to be destroyed.

*"Is this behind the schema?"* is a question about **provenance**, and it is
answerable without ever hashing the file on disk: compare
`sha256(ddl.Emit(schema))` against the checksum recorded when the migration was
created. A hand edit to the migration does not move that value; a schema change
does. `ddl.Emit` is already pure, total and deterministic (§5.3, §8), so
re-emitting it on every run costs nothing and needs no database.

That separation is what makes §6.1's *warning* and this section's *hard stop*
both true at once. The migration is never overwritten, so asking the hand-edit
question about it manufactures a stop with nothing to protect — and stops on
exactly the state open question 4 already contemplates as normal and tolerated.

| Situation | Kind | Behaviour |
|---|---|---|
| File present **and** its checksum differs from the lock | `generated` | Stop, name the file, exit non-zero. `--force` overrides. |
| File present **and** the *emitted* checksum differs | `emitted-once` | Warn, name the file, never write, exit 0. §6.1 enumerates all four of this kind's states. |
| File absent, entry present | `generated` | Proceed. `rm -rf internal/gen` is the documented way to force a clean regeneration, and a file that is not there has no work to lose. |
| File absent, entry present | `emitted-once` | Stop, name the file, say it must be restored from version control. `--force` re-emits it. |
| A file in `internal/gen` the lock has never seen | — | Stop. It is either a hand-added file or a stale artifact; the tool does not guess. |
| Lock absent, `internal/gen` absent | — | First run. Proceed. |
| Lock absent, `internal/gen` present | — | Stop. Nothing is known about that content, and rename-aside would destroy it. `--force` overrides. |

Row 1 says *present **and** differs* on purpose. Revision 2 wrote "file checksum
differs from the lock", which is literally true of a file that is not there at
all, so `lapigo gen` refused and demanded `--force` in the one state where
regenerating is obviously correct — after deleting `internal/gen`, or in a clone
where it is gitignored.

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

#### What the generated packages declare

"Collision with an identifier the generated package already exports" was
unenumerated, which made it unimplementable: the merged validator checks entity
against entity and entity against enum type, and derived names collide with
nothing. Entity `article` yields `ArticleCreateInput`; an entity named
`article_create_input` yields the same identifier, in the same package, and the
schema validates clean.

**§6.1's file tree is authoritative: generated code lives in four packages, not
one.** §6.3's user-side example wrote `gen.NoopArticleHooks`, which implies a
single package and a single collision domain; that example was wrong and is
corrected in §6.3. The distinction is not cosmetic — it decides the answer.

**"Declaration" here means every top-level declaration `go/ast` reports** —
types, functions, variables and constants, exported or not, **and methods**. A
method is an `*ast.FuncDecl` with a non-nil `Recv`, and it is in the set on
purpose: `internal/validate`'s `reservedMethodNames` is a list of *method* names
(`internal/validate/names.go`, documented as methods on the model and the input
types), so a table that predicted only types would leave §10's debt exactly
where it was. Unexported names are in the set for the opposite reason — a
generated `scanArticle` row helper is a real declaration and its drift is real
drift — and including them costs the collision check nothing, since a
schema-derived name is always export-cased and can never equal one.

For every entity with Go name `E`, and every enum field of that entity with Go
name `F`, **conditioned on that entity's endpoint set** (§6.2's escape hatch 4
means the set is not fixed):

| Package | Always | Only when |
|---|---|---|
| `model` | `E`, `EF`, one `EF<Value>` per enum member | `ECreateInput` (step 6) and `ECreateInput.Validate` (step 7) — `HasCreate()`; `EUpdateInput` (step 6) and `EUpdateInput.Validate` (step 7) — `HasUpdate()` |
| `store` | `EStore`, `scanE` | `EListQuery` and `EStore.List` — `HasList()`; `EStore.Get` — `HasGet()`; `EStore.Create` — `HasCreate()`; `EStore.Update` — `HasUpdate()`; `EStore.Delete` — `HasDelete()` |
| `hooks` | — | `EHooks`, `NoopEHooks`, and one method trio per generated write operation — `HasCreate()`, `HasUpdate()`, `HasDelete()` respectively |
| `httpapi` | lands with step 8 | — |

The conditions are not decoration. The fixture-matrix rule below requires a
fixture with each `endpoints:` member **absent**, so an unconditional row would
fail direction 2 on the first such fixture — `ECreateInput` predicted and not
emitted for an entity that declares no `create`.

`Validate` is emitted on the input types and **not** on the model, which is
output-only. `internal/validate`'s `reservedMethodNames` nonetheless reserves it
across the model as well, because the same schema field name becomes a Go field
on all three types; that list is deliberately a superset of what is emitted, and
the equality test compares the emitted set, not the reserved one.

Each package also declares a **fixed** set that does not vary with the schema,
and every package has one:

| Package | Fixed |
|---|---|
| `model` | `Optional[T]` and its methods (§6.5) |
| `store` | the cursor encoder, decoder and decode-error type (§7.4) |
| `hooks` | `Error`, its `Error` method, `NewValidationError` (§6.4) |
| `httpapi` | the router constructor (§6.2), the request-id middleware (§6.9.6), `respondError` (§6.7) |

Those names are part of the same comparison, both as identifiers a
schema-derived name may not collide with and as declarations the equality test
must predict — a per-entity table alone would fail direction 1 on the first
package-level helper anyone writes.

**A package whose templates do not exist yet is listed as pending, and
contributes no fixture to the matrix until it does.** `store` lands at step 7 and
`httpapi` at step 8 (§10); the commit that adds each one fills in its rows in
the same change, which is the only moment at which the two can be written
together and known to agree. `model`'s conditional row splits across two
commits rather than landing whole with one package: `ECreateInput`/
`EUpdateInput` (the struct types) move to step 6, alongside `Optional[T]`,
because §6.3's hook signatures name them and a step whose own package cannot
type-check is not a step boundary (§10's amended step 6 row); `Validate`
stays step 7's, since nothing about hooks needs it.

Two properties of that set are load-bearing:

- **Collisions are compared *within* a package, never across.** `Article` in
  `model` and `ArticleStore` in `store` are different packages and do not
  collide, and neither do `EHooks` and a hypothetical `model` type of the same
  name. "Reject, never mangle" licenses rejecting an *ambiguous* schema; it does
  not license rejecting a legal one, and a cross-package comparison would.
- **`NoopEHooks` is a prefix form.** An implementation that checks "does this
  name end in one of the known suffixes" is wrong, silently, for exactly one
  entry in the table — the one whose entity name lands at the end.

A derived name is attributed to the entity's `NameSpan` (or, for an enum
constant, the value's own span per the 2026-09-09 amendment), so the diagnostic
still blames a line in the schema with a caret under the token that caused it.
A generated name has no position of its own; the schema token that produced it
does.

#### Linking the table to the templates

§10 records this as a debt from step 3 and states the mechanism as "derive the
reserved set from the templates". **That is not mechanically possible.**
Template files are `text/template` text, not parseable Go — `go/parser` rejects
them — and the identifier a template emits is a function of runtime data
(`type {{.GoName}}Store struct`), so no static read of the template text yields
`ArticleStore`.

What works is the inverse. Render a fixture matrix, parse the **output** with
`go/ast`, and assert **set equality, per package, in both directions**:

1. every top-level declaration in the rendered output is predicted by the table
   above;
2. every name the table predicts for those fixtures is present in the output.

Equality, not subset. A subset check in direction 1 alone passes when the table
lists a name no template emits; in direction 2 alone it passes when a template
emits a name the table has never heard of — which is the drift this test exists
to catch. Only both together fail on both.

**The fixture matrix rule:** for every schema option that changes *which*
declarations are emitted — each `endpoints:` member, an enum field, a
`belongsTo` — the matrix contains a fixture with it present and a fixture with
it absent. A declaration emitted only for, say, entities that declare `update`
is invisible to a matrix whose fixtures all declare it, and invisible in a way
that looks exactly like a pass. A further test asserts that the matrix covers
each option in both states, so adding an option without extending the matrix
fails rather than quietly narrowing the coverage.

`version:` is deliberately **not** in that list, and the omission is the
point: it changes the *bodies* of `model` and `store` declarations — an extra
column, an extra `WHERE` predicate — and not the set of them, so it is not a
declaration-changing option there. It may become one in `httpapi`, where §6.6's
`If-Match` parsing lands at step 8; if that commit emits a version-conditional
declaration, it adds the row and adds `version:` to this list at the same time.
It stays in the golden-file matrix regardless, because a changed body is exactly
what a golden file is for.

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

#### `lapigo new`'s module files

`go.mod` is written with the module path, the Go version, and
`github.com/jackc/pgx/v5` at the version pinned into the lapigo binary at build
time (§2.3) **plus pgx's own indirect requirements**, and `go.sum` is written
from bytes embedded in the binary. Neither is computed per run and neither
touches the network. Both halves are necessary, measured:

- without the `go.sum` lines, `go build` fails with `missing go.sum entry`;
- without the indirect requires, it fails with `updates to go.mod needed`.

§11 asserts that `lapigo new demo && lapigo gen && psql -f … && go run ./cmd/api`
works with no `go mod tidy` and no network step, and these files are what makes
that assertion true rather than aspirational.

#### `migrations/0001_init.sql`

The migration is written by step 9's CLI, not by `Generate` (§5.4), and it is
covered by an `emitted-once` lock entry (§5.5) recording the SHA-256 of what
lapigo emitted when it created the file — never of the file on disk. Four
states, exhaustively:

| State | Behaviour |
|---|---|
| File absent, no lock entry | Write it, record the emitted checksum. |
| File present, `sha256(ddl.Emit(schema))` equals the recorded checksum | Silent. The schema has not moved. |
| File present, the emitted checksum differs | **Warn**, name the file, point at phase 2's diffing. Never write, never stop, exit 0. |
| File absent, lock entry present | **Stop.** Say it must be restored from version control; `--force` re-emits it. |

Row 3 is the case that matters. The migration is applied history: rewriting it
silently is how a generator corrupts a production database, and stopping on it
would break the workflow open question 4 already treats as normal — a hand-edited
migration is a tolerated state, not an error. Because the comparison is against
what lapigo *emitted*, a hand edit does not move it and does not false-warn; only
a schema change does.

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

The interface is stated in full, for all five operations. Revision 2 showed
`Create` and wrote "same shape for Update and Delete", which is not a shape
anyone can copy: `Delete` has no input type to be the analogue of
`*ArticleCreateInput`, and there is no `DeleteInput` anywhere in this document.

```go
// generated — package hooks
type ArticleHooks interface {
    BeforeCreate(ctx context.Context, tx pgx.Tx, in *model.ArticleCreateInput) error
    AfterCreate(ctx context.Context, tx pgx.Tx, a *model.Article) error
    AfterCreateCommitted(ctx context.Context, a *model.Article)

    BeforeUpdate(ctx context.Context, tx pgx.Tx, in *model.ArticleUpdateInput) error
    AfterUpdate(ctx context.Context, tx pgx.Tx, a *model.Article) error
    AfterUpdateCommitted(ctx context.Context, a *model.Article)

    BeforeDelete(ctx context.Context, tx pgx.Tx, id pgtype.UUID) error
    AfterDelete(ctx context.Context, tx pgx.Tx, a *model.Article) error
    AfterDeleteCommitted(ctx context.Context, a *model.Article)
}

type NoopArticleHooks struct{}
func (NoopArticleHooks) BeforeCreate(context.Context, pgx.Tx, *model.ArticleCreateInput) error { return nil }
// ...
```

```go
// yours
type ArticleHooks struct{ hooks.NoopArticleHooks }

func (h ArticleHooks) BeforeCreate(ctx context.Context, tx pgx.Tx, in *model.ArticleCreateInput) error {
    title, _ := in.Title.Get()      // mandatory, so Validate has already run
    in.Slug.Set(slugify(title))     // readonly: off the wire, still settable here
    return nil
}
```

The user-side example imports `hooks` and `model`, not a single `gen` package.
Revision 2 wrote `gen.NoopArticleHooks` and `gen.ArticleCreateInput`, which
contradicts §6.1's own file tree — four packages, `model`, `store`, `httpapi`
and `hooks` — and, worse, implies a single collision domain for §5.6's name
check. §6.1's tree is authoritative.

**`BeforeDelete` receives the id, not the row.** `id` is typed as the primary
key's Go type (§3.2), so `pgtype.UUID` here and `string` for a `slug` key. A
pre-image would have to be loaded, and the store cannot know at generation time
whether a given user's `BeforeDelete` is still the no-op, so it would have to
emit `SELECT … FOR UPDATE` before **every** delete of **every** entity —
a blanket cost on every user of the generator to serve a hook most of them never
implement. That is the reasoning that rejected `COUNT(*)` and offset pagination,
applied to hooks. "What was deleted" logic belongs in `AfterDelete` and
`AfterDeleteCommitted`, which receive the full row from the store's
`DELETE … RETURNING` — already loaded, already free.

**`BeforeUpdate` receives no pre-image either**, for the same reason, and it
does not need one to answer the question hooks actually ask: *was this field
submitted?* `in.Title.Present()` answers it, because `ArticleUpdateInput`'s
members are `Optional[T]` (§6.5). An earlier analysis justified this with
"`in.Title != nil` already does this work — §6.6's absent-versus-null pointer
design". That pointer design does not exist and cannot: §3.2 records the
measurement. The conclusion stands; the mechanism is `Optional[T]`, not a
pointer.

**There are no `List` or `Get` hooks in phase 1**, and that is a decision, not an
omission. Every hook above takes a `pgx.Tx` because hooks run inside the store's
write transaction; a read has no such transaction, so a read hook is an
unanswered signature question — does it get a `pgx.Tx`, a `pgx.Conn`, nothing? —
rather than a copy-paste of the write shape. And the payoff people want from a
list hook is mostly interception of relation expansion, which is deferred to 1.5
(§1). Adding them later is not a breaking change, because embedding the no-op is
what makes the interface evolvable, below.

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

**"Can abort" needs a way to say *how*.** Revision 2 designated `BeforeX` as the
place for validation and gave a returned error nowhere to land: §6.7's table had
no row for it, so it fell through to *"anything else → 500 `internal`"* and a
user's own validation rejection was emitted to their client as a 500 with a
correlation identifier and no detail. That breaks §11's fourth criterion in
spirit — the envelope is technically respected and the response is useless.

```go
// generated — package hooks
type Error struct {
    Status  int               // HTTP status
    Code    string            // the envelope's `code`
    Message string            // the envelope's `message`
    Fields  map[string]string // the envelope's `fields`; may be nil
}

func (e *Error) Error() string

// NewValidationError fills Status 422 and Code "validation_failed".
func NewValidationError(message string, fields map[string]string) *Error
```

A hook returns `*hooks.Error` to choose its own status and code; it returns any
other error to get the 500 it deserves. `respondError` matches it with
`errors.As` (§6.7) and **clamps a `Status` outside 400–599 to 500**. A hook is
user code, `Status` is an untyped `int`, and `w.WriteHeader(0)` panics — trusting
a user's integer blindly would turn a typo in an extension point into a crashed
request. The clamp is silent to the client and logged.

### 6.5 Input projection — what a client may set

This is the mass-assignment rule, stated as a rule rather than implied.

**It is two independent questions, not a subtraction and not one table.**
Revision 2 defined `CreateInput` by removing fields from the field list and
`UpdateInput` by removing fields from `CreateInput`, and a subtraction has
exactly one knob per field: it can remove, never re-add or mark optional. Two of
the three defects settled here were manufactured by that shape — `default:`
propagating its create-time exclusion into the update input (§3.1), and the
version column having no row to be excluded by (§3.6).

Replacing it with a *single* first-match table then manufactured a third, caught
in review. A field carrying both `required: true` and `immutable: true` matched
the `immutable:` row and came out optional on create — a NOT NULL column with no
default, omitted by the client, inserted as NULL, `23502` back from Postgres,
which §6.7's table does not map, hence a `500` on every create from a schema
that validates clean. Presence and requiredness are answered separately below
because they are separate facts.

**Question 1 — is the member there?** First matching row wins:

| Field carries | `CreateInput` | `UpdateInput` |
|---|---|---|
| `pk` | absent | absent |
| `version: true` | absent | absent |
| `readonly: true` | member, `json:"-"`, off the wire | member, `json:"-"`, off the wire |
| `immutable: true` | present | absent |
| anything else | present | present |

**Question 2 — must the client send it?** Asked only of a member that is on the
wire, and answered identically for both input types:

> A member is **mandatory** exactly when its field is `required: true` and
> carries no `default:`. Every other member is **optional**.

*Mandatory* means `Validate` returns a 422 when the value is missing —
`!Present() || IsNull()` on `CreateInput`; `IsNull()` alone on `UpdateInput`,
where an absent key means unchanged (§6.6). *Optional* means an absent key is
allowed, because `default:` supplies the value or the column is nullable.

Together the two questions reproduce every projection the old single table
listed, and fix the one it got wrong: `{required: true, immutable: true}` is
present and **mandatory** on create and absent from update, which is what an
`author` that is set once and never changed has always needed.

#### The combination the validator rejects

Question 2 is only answerable for a member that is on the wire. For one that is
not, something else has to supply the value on insert, and for exactly one
option nothing does:

> A field that is `required: true`, carries no `default:`, and is **absent from
> `CreateInput`** is a schema error, reported with a position on the field.

That is `readonly: true`, and only it. `pk` is exempt because §3.2 has the
generator mint the key client-side; `version: true` is exempt because the store
inserts version 1 (§6.6). In both, lapigo itself supplies a value on every
insert. A `readonly:` field has only a hook to fall back on, and the generator
cannot know at generation time whether the user wrote one — the default is the
no-op — so the honest reading is that nothing supplies it. The recourse is one
word: give the field a `default:`, which `BeforeCreate` may still overwrite
through the member below.

This is the same shape as §13's `on_delete: set_null` rule — the DDL emits
exactly what was asked, applies cleanly, and then fails at runtime on a schema
that validated clean — and it gets the same treatment.

`pk` is absent because §3.2 generates `uuid` keys client-side in Go and a client
naming its own primary key is how duplicate-key 500s and enumeration attacks
begin. `readonly:` is the option that means *never accepted from a request*, and
it is why §3's example writes `created_at: { default: now, readonly: true }`:
without this rule a client could set `created_at` — the sort key — and insert
itself at an arbitrary position in every cursor page. `version: true` is absent
because a client that can `PATCH` its own version defeats `If-Match` entirely
(§3.6); note that this is a row, not a synthesized `readonly:` flag, for the
reason §3.6 gives.

**`readonly:` is off the wire, not out of the struct.** §6.4 designates the
`BeforeX` hooks as the place for derived fields — `Update` as much as `Create` —
and §6.3's own example sets a `slug` that §3's schema marks `readonly: true`. A
hook cannot set a member that does not exist, so a `readonly:` field carries a
Go member in **both** input types, tagged `json:"-"` and `Optional[T]` like
every other member. That is what lets `BeforeUpdate` maintain the slug when the
title changes — `in.Slug.Set(slugify(t))` marks it present and the store
includes the column. Without the member in `UpdateInput`, this document's own
example could derive a slug once and never again.

The tag is what keeps the projection honest, and it was checked rather than
assumed: `json:"-"` removes the field from the decoder's key set entirely, so
`DisallowUnknownFields` rejects an inbound `{"slug": "…"}` with a 400 — the
assertion §8 already lists — and there is no mass-assignment hole. `pk` and
`version: true` carry no member at all, because nothing on the write path should
set either.

#### Input members are `Optional[T]`, not pointers

```go
type Optional[T any] struct {
    value   T
    present bool // the key appeared in the JSON object at all
    null    bool // the key appeared, carrying the literal null
}

func (o Optional[T]) Present() bool  // absent (false) vs. sent (true, incl. null)
func (o Optional[T]) IsNull() bool   // sent, carrying null
func (o Optional[T]) Get() (T, bool) // value, and present && !null
func (o *Optional[T]) Set(v T)       // for hooks: present, not null, value v
func (o *Optional[T]) SetNull()      // for hooks: present, null
func (o *Optional[T]) UnmarshalJSON([]byte) error
```

Three states, three answers. `UnmarshalJSON` **is** invoked for the literal
`null`, which is the whole mechanism: absent leaves `present` false and the
method is never called; `null` sets `present` and `null`; a value sets `present`
and `value`. A pointer cannot do this and neither can `**T` — §3.2 records the
measurement and the earlier false claim. `DisallowUnknownFields` is unaffected:
it rejects keys the struct does not declare, before any member's
`UnmarshalJSON` runs.

**`Get`'s second result is `present && !null`, not `present`.** It is pinned
here because it is a method on code shipped to every user, and the plausible
reading — "was a key sent" — makes `{"body": null}` return `("", true)` and
hands a caller an empty string it will write as data. The one question `Get`
answers is *may I use this value*, and a `null` is not a value; a caller
distinguishing absent from null asks `Present()` and `IsNull()`, which exist for
exactly that.

`Set` and `SetNull` exist for hooks, which have no JSON to decode. Without them
a `BeforeUpdate` cannot write to a member at all, and §6.4's "derived fields"
would be a create-only facility.

The store selects the columns for its `UPDATE` from `Present()`, never from a
nil check, and writes `NULL` for a member where `IsNull()` holds — rejected with
422 when the member is mandatory (question 2 above).

#### The Go type of an input member

**Every member of both input types is `Optional[T]`**, mandatory ones included.
A mandatory member typed as a bare `string` cannot be validated at all: `{}` and
`{"title": null}` both leave it `""` and neither returns an error from
`encoding/json`, so `Validate` could not tell a missing title from an empty one —
the same trap as the pointer, one type further along.

`T` is the field's **value** type: `string`, `int32`, `time.Time`. It is **not**
`ir.Field.GoType()`, which returns `*string` for a nullable field because it
answers for the *model* (§3.2). Templates may not compute the difference
themselves (§5.1), so `Field` gains a second accessor — `ValueGoType() string`,
the type with nullability stripped — in the same omission class as the
`Method()`, `SQL()`, `SortedFilters()` and `Has*()` accessors §13 records for
issue #25. Both stay derived, never stored (§2.2).

#### Where input validation lives, and when it runs

A generated `Validate() error` method on each input type, in **`model`** — the
name `internal/validate`'s `reservedMethodNames` already reserves, and the
package holding the `required:`, `max:` and enum-membership facts it checks.

The order in a write handler is fixed: **decode, `Validate`, open the
transaction, `BeforeX`.** Validation runs before a transaction is opened, so a
malformed request never costs one; and by the time a hook runs, every mandatory
member is `Present()` and not `IsNull()`, which is what lets §6.3's example read
`in.Title.Get()` and discard the second result.

`Validate` returns a `model`-owned error type, never `*hooks.Error`. `hooks`
imports `model` for every signature in §6.3, so a `model` that returned
`hooks.Error` would close an import cycle. `httpapi` imports both and maps each
to the envelope: a validation error to 422 `validation_failed`, a `*hooks.Error`
to its own status (§6.7).

JSON decoding uses `DisallowUnknownFields`: a request carrying a field the input
does not accept is a 400, not a silent drop. A client discovering that
`created_at` is ignored is better served by an error than by a surprise. The
query-parameter analogue is in §6.9.4.

### 6.6 Update semantics

`PATCH` only; `PUT` is not generated. Absent means unchanged, explicit `null`
means set to null (rejected for `required` fields). The mechanism that
distinguishes those two states is `Optional[T]`, specified in §6.5 — not a
pointer.

**Optimistic concurrency is opt-in via `version: true`.** When absent, updates
are last-write-wins — documented, not accidental. When present, the following
grammar applies, and it is generated code, not a convention:

- **`ETag: "<version>"`** on **every single-resource response** of a versioned
  entity — the 200 from get, the 201 from create, the 200 from update. Not on a
  list response: a list is not a resource with one version. The tag is
  **strong** — no `W/` prefix — because it is an exact value comparison against
  a column, not a claim about semantic equivalence of two renderings.
- **`If-Match` is required on `PATCH`.** Absent is `400` `bad_request`, with
  `If-Match` named in `fields`. Phase 1 does not emit `428 Precondition
  Required`: a client that omitted the header and one that sent a broken one
  have both sent a request lapigo cannot act on, and one status per cause keeps
  §6.7's table something an implementer can follow.
- **`If-Match: *` is accepted** and means "any current version": the update
  proceeds without a version predicate. It is the honest way to say *I know I am
  overwriting*, and refusing it would push people to read the ETag and echo it
  back, which is the same thing with an extra round trip.
- **A weak tag (`W/"3"`), a list of tags, or an unparseable value is `400`.**
  Never a fallthrough to unconditional update: a client that sent a header it
  believed was protecting it must not be told the write succeeded unprotected.
- **A mismatch is `409` `version_conflict`.** The store emits
  `UPDATE … SET …, version = version + 1 WHERE <pk> = $1 AND version = $2`, and
  **zero affected rows is a 409**. It is not distinguished from a deleted row:
  telling the client which of the two happened requires a second query and leaks
  a little more than it helps; the client re-reads either way.
- **Create inserts version 1.** Not 0, so that "has a version" and "has been
  written once" are the same statement, and not a client-supplied value —
  §6.5 keeps the column out of `CreateInput`.
- **`DELETE` on a versioned entity also requires `If-Match`**, with the same
  grammar and the same `409`. Symmetry is the reason: an API where a lost update
  is refused but a lost *delete* succeeds silently protects the smaller of the
  two losses. Recorded plainly because it widens this decision's blast radius —
  it puts a required header on an operation that had none, so `DELETE` on a
  versioned entity is a breaking change for any client written against the
  unversioned shape, and step 8's delete handler grows the same parsing and the
  same `409` path as update.

An update that changes a sort key is permitted and warned about at generation
time (§3.3 rule 5).

### 6.7 Errors

One envelope, everywhere:

```json
{ "error": { "code": "validation_failed",
             "message": "title is required",
             "fields": { "title": "required" },
             "request_id": "9f2c1e4a7b0d3f6812a5c9e0d4b7f13a" } }
```

| Condition | Status | `code` |
|---|---|---|
| Malformed body, unknown field or query parameter, bad cursor, bad limit, unparseable filter value, bad `If-Match` | 400 | `bad_request`, `invalid_cursor` |
| Request body over the limit (§6.8) | 413 | `body_too_large` |
| Missing or non-JSON `Content-Type` on `POST`/`PATCH` | 415 | `unsupported_media_type` |
| Validation failure | 422 | `validation_failed` |
| Not found | 404 | `not_found` |
| Unique or FK violation (`23505`, `23503`) | 409 | `conflict` |
| `If-Match` mismatch | 409 | `version_conflict` |
| A hook returning `*hooks.Error` | its `Status`, clamped to 400–599 | its `Code` |
| Anything else | 500 | `internal` |

A `500` body carries the code and the request identifier, never a driver
message, a query, a constraint name or a path. The detail is logged. This is
mechanism, not exhortation: handlers call one `respondError` helper, and a
`pgx` error never reaches a response except through the mapping above.
`respondError` tries `errors.As(err, &he)` for `*hooks.Error` **before** the
generic branch, and applies §6.4's status clamp.

**`request_id` is a fourth member of the envelope on every error, not only on a
500.** Revision 2 attached a correlation identifier to the 500 alone, which is
backwards: a 500 is the case a user reports and someone goes looking for; a 400
they cannot explain is the case they argue about. It costs four bytes of key and
it is what turns "it returned 422 and I do not know why" into a log lookup. Its
minting is specified in §6.9.6, together with the `X-Request-Id` header that
carries it on **every** response, success included.

`fields` is omitted when empty rather than emitted as `null` — it is the one
member of the envelope that is genuinely optional, because most errors have no
per-field detail. `code`, `message` and `request_id` are always present.

**413 is its own status, not a 400.** Every reverse proxy in front of a
generated API — nginx, Caddy, an ALB — already answers an oversized body with
413 from its own limit. Folding lapigo's limit into 400 would make one cause
produce two different status codes depending on which limit was crossed first,
which is precisely the kind of thing that costs an afternoon.

**`http.MaxBytesReader`'s error is wrapped, and the order of the branches
decides whether the 413 ever appears.** The reader returns
`*http.MaxBytesError`, but `json.Decoder.Decode` wraps it before the handler
sees it, so a handler that tests for a decode failure first classifies every
oversized body as a malformed one and the 413 becomes unreachable. The generated
handler runs `errors.As(err, &maxErr)` **before** the generic decode branch.

### 6.8 Resource bounds

Generated code, not advice in documentation:

- `http.MaxBytesReader` on every request body, default 1 MiB. Exceeding it is
  `413` `body_too_large`, and §6.7 states the `errors.As` ordering that makes
  that status reachable at all.
- `context.WithTimeout` on every query, default 5 s.
- Scaffolded `main.go` sets `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`
  and `IdleTimeout`.
- `limit` defaults to 20 and is capped at 100. Out-of-range, zero, negative or
  unparseable values are **rejected with 400** — not clamped. Silent clamping
  hides client bugs, and revision 1 specified both behaviours in one sentence.

An uncapped request body is the cheapest denial of service against the 4 vCPU
box this project targets.

### 6.9 The wire contract

§6.7 defines what failure looks like. This section defines everything else, and
it exists because revision 2 defined none of it: not the list envelope, not the
success status codes, not the query parameter names, not the filter syntax, not
the rule mapping a schema field to a JSON key. Those are the public API of every
project ever generated by this tool, and they were spread between `CLAUDE.md`, a
French design note and nothing at all. The bar for this section is that two
implementers working from it produce **byte-identical responses**.

#### 6.9.1 Routes

```
GET    /articles            list
POST   /articles            create
GET    /articles/{id}       get
PATCH  /articles/{id}       update
DELETE /articles/{id}       delete
```

**The collection path is `"/" + Entity.Table`.** This is a ratification of the
convention `internal/parse/endpoint.go` invented at step 2, not a fresh choice,
and it holds for two reasons. `table:` is currently the *only* lever a schema
has over its URLs, so deriving the path from `Entity.Name` instead would remove
the one knob and add none. And the entity name would not buy the expressiveness
people imagine it would: both names go through the same identifier grammar
(`^[A-Za-z_][A-Za-z0-9_]*$`, `internal/parse/names.go`), which forbids `-` and
`/`, so `/blog-posts` and `/v1/articles` are unreachable from either. The
widening is an entity-level `path:` key, and it is **deferred to phase 2** as a
decision, not overlooked: it wants to carry a leading-slash rule, a collision
check against every other entity's path, and a rule for what happens to
`{`-bearing text, none of which phase 1 needs.

**The wildcard is named after the primary key column**, not hardcoded to `id`.
`Entity.Path(EndpointGet)` yields `/articles/{id}` only because that entity's PK
column happens to be `id`; an entity keyed on `slug` is served at
`/articles/{slug}` and its handler reads `r.PathValue("slug")`. A handler that
reads `PathValue("id")` and binds the result to a `slug` column is a line of
generated code that lies to the person reading it, and §6.2 already records that
wildcard names are irrelevant to `ServeMux` conflict detection — so honesty here
is free.

`Path` is a method on `Entity`, never a stored field on `Endpoint` (§2.2).

#### 6.9.2 JSON keys are column names

**One vocabulary, used everywhere a field is named on the wire**: body keys in
requests and responses, filter query parameter names (§6.9.4), and cursor
members (§7.4) all use `Field.Column`.

So a `belongsTo` written as `author:` in the schema appears on the wire as
`author_id` — its column — which is what it is: a foreign key scalar. That
choice also reserves `author` for phase 1.5's relation expansion, where every
ecosystem that has solved this puts the expanded object. Having `author` mean
the scalar in phase 1 and the object in 1.5 would be a breaking change scheduled
in advance.

**No camelCase conversion.** Columns are snake_case, so converting would invent
a fourth name for a field that already has three (`Name`, `Column`, `GoName`),
and the conversion is not even well defined: `x__y` has no unambiguous camel
form and no unambiguous inverse. One name, no round-trip.

Two mechanical consequences for the templates:

- **Every generated struct field carries an explicit `json:"…"` tag.** Never
  relying on Go's default, which is the Go field name and therefore
  `AuthorID` — a fourth spelling arriving by omission.
- **No `omitempty` anywhere in a model.** A nullable column holding NULL must
  serialise as `"body": null`; with `omitempty` it vanishes from the object
  entirely, and a client cannot distinguish "no body" from "this field does not
  exist in this version of the API". §6.7's `fields` is the single documented
  exception, and it is in the error envelope, not a model.

#### 6.9.3 Success responses

**List** — `200`:

```json
{ "items": [ { "id": "…", "title": "…" } ],
  "next": "eyJ2IjoxLCJlIjoiYXJ0aWNsZSI…" }
```

- **`items` is never `null`.** A nil Go slice marshals to `null`, and a client
  looping over `null` is a crash in every language that has ever consumed a JSON
  API. The store returns a made slice — `make([]T, 0, limit)` — so an empty page
  is `[]`. This is a property of the store, not of the handler: a handler-side
  `if items == nil` is a patch that the next code path forgets.
- **`next` is always present**, and is `null` on the last page. **No
  `omitempty`.** A client testing `"next" in response` must be able to trust the
  key's existence; a cursor that disappears when it is empty makes "last page"
  and "old server" the same observation.
- **A single resource is the bare object**, not `{"item": …}`. There is nothing
  to put beside it, and a wrapper that carries no second member is ceremony.

**Get** — `200`, the object.

**Create** — `201`, the **full object** in the body, plus `Location`.

- `Location` is the **detail path** for the created row — `Entity.Path` for the
  `Get` kind with the wildcard replaced by the primary key value, passed through
  `url.PathEscape`. Escaping is not optional: a `slug` primary key can contain
  `/`, `?` or `#`, and an unescaped one produces a header that points somewhere
  else entirely.
- It is **relative** (`/articles/9f2c…`), not absolute. Building an absolute URL
  requires trusting `Host` and `X-Forwarded-Proto`, which is a header the client
  controls; a generator that ships to everyone must not bake in a
  reflected-host redirect.
- **No `Location` when the entity declares no `get` endpoint.** `endpoints:
  [list, create]` is legal (§6.2), and a `Location` pointing at a 404 is worse
  than none. `201` without `Location` is legal HTTP.

**Update** — `200`, the full object.

**Delete** — `204`, **no body**, and no `ETag`: there is no resource left to
tag. Deleting a row that is not there is **`404`**, not an idempotent `204`.
Phase 1's delete is a single `DELETE … RETURNING` whose zero-row result the
store already has to inspect, so the 404 costs nothing; and a 204 for an absent
row hides a client bug — the same reasoning §6.8 uses to
reject clamping an out-of-range `limit`.

On a versioned entity, every single-resource response above — the `200` from
get, the `201` from create, the `200` from update — also carries an `ETag`, and
`PATCH` and `DELETE` require `If-Match`. §6.6 states that grammar and its `409`.

#### 6.9.4 Query parameters

Flat, one parameter per filter, no operator syntax and no nesting:

```
GET /articles?status=draft&author_id=9f2c…&limit=20&after=eyJ2Ijox…
```

- **Filter parameter names are column names** (§6.9.2), drawn from the entity's
  declared `filters:` — the whitelist §3.4 already defines. A request selects
  which whitelisted column; it can never name one.
- **`limit` and `after` are reserved wire names.** A filter whose column
  resolves to either is a **validator error with a position** on the `filters:`
  entry, per §5.6's "reject, never mangle". The alternative is renaming the
  user's parameter behind their back, which makes the wire name a function of
  the generator version — the same failure mangling causes for Go identifiers.
- **`limit`** defaults to 20 and is capped at 100, rejected rather than clamped
  (§6.8). **`after`** carries the opaque cursor (§7.4); any decode failure is
  `400 invalid_cursor`.

**Per-type value parsing.** Every failure below is a `400`, with the parameter
name in `fields`. There is no lenient path — a filter value lapigo cannot parse
is not "no filter".

| Type | Accepted | Rejected |
|---|---|---|
| `uuid` | canonical `8-4-4-4-12` hyphenated form | braces, `urn:uuid:`, unhyphenated |
| `int` / `bigint` | `strconv.ParseInt` at the column's bit size | anything out of range — overflow is a 400, never a silent wrap |
| `float` | a finite `strconv.ParseFloat` | `NaN`, `Inf`, `-Inf` |
| `bool` | exactly `true` or `false` | `1`, `t`, `TRUE`, and the rest of `ParseBool`'s laxity |
| `decimal` | a numeric literal `pgtype.Numeric` accepts | — |
| `timestamp` / `date` | RFC 3339, normalised to UTC before binding | anything else |
| `enum` | one of the field's declared values | everything else |
| `json` | — | not filterable at all (open question 2) |

Three of those rows are not fussiness:

- **`NaN` and `Inf` compare false against every value including themselves**, so
  `?score=NaN` would return an empty page with a `200` and no indication that
  the filter was meaningless. Rejecting at the edge is the only place the user
  finds out.
- **`ParseBool`'s laxity** would make `?published=1` work and `?published=yes`
  fail, which is a rule nobody can remember and no client library agrees on.
- **An undeclared enum value is the worst available outcome if accepted**:
  `?status=drfat` returns `200` with zero rows, and the typo is indistinguishable
  from an empty result. `400` names the parameter and the schema already holds
  the list of legal values, so the message can print them.

**A repeated parameter is a `400`.** `url.Values.Get` silently returns the first
value and discards the rest, so `?status=draft&status=published` would filter on
`draft` and throw the user's second intent away without a word. The handler
checks `len(values[k]) > 1` before reading.

**An empty value is a `400`.** `?status=` is neither "unfiltered" nor
`status IS NULL`. Treating it as unfiltered makes a client bug look like a wider
query; treating it as `IS NULL` is worse, because it needs a per-parameter
branch in the SQL, and **§7.3 bans exactly the idiom that branch reaches for**
(`($1 IS NULL OR col = $1)`, measured at a 66-fold regression). Stated plainly so
nobody reinvents it: **phase 1 offers no way to filter for NULL.** A schema that
needs one declares a boolean companion column, or waits for the operator syntax
in a later phase.

**An unknown query parameter is a `400`**, with the offending name in `fields`.
§6.5's justification for `DisallowUnknownFields` transfers verbatim — a client
discovering that a parameter is ignored is better served by an error than by a
surprise, and a typo'd filter name that silently widens a query is a data leak in
the shape of a feature. Honestly: this means that pasting a URL carrying
`utm_source` into a generated API returns a `400`. That is the cost, it is
accepted, and it is written down here rather than discovered.

#### 6.9.5 Request preconditions

**`POST` and `PATCH` require `Content-Type: application/json`**, or `415`
`unsupported_media_type`. The header is parsed with `mime.ParseMediaType`, not
compared as a string, so `application/json; charset=utf-8` — which several HTTP
clients send by default — is accepted; only the media type is compared, and it
is compared case-insensitively as the RFC requires. A naive `==` would reject
half the world's clients for a suffix that means nothing.

Requiring the header at all is CSRF hygiene: `application/json` is not a content
type an HTML form can produce, so a generated API is not driveable by a
cross-origin form post even before phase 3 adds auth.

`GET` and `DELETE` carry no body and no `Content-Type` requirement.

#### 6.9.6 Request correlation

**Every response carries `X-Request-Id`**, success and failure alike, and every
error envelope repeats it as `request_id` (§6.7).

**It is minted by a middleware, into the request context** — not inside
`respondError`. That is the whole point: a log line written while the query ran,
before anything failed, has to carry the same identifier as the response the
user is holding. An identifier minted at the moment of responding cannot
correlate anything that happened earlier in its own request, which is most of
what a person is looking for.

**The middleware is exported separately and wraps the mux from outside; the
router constructor keeps returning `*http.ServeMux`.** A `ServeMux` cannot run
anything ahead of its own dispatch, so the three options were: wrap inside the
constructor and return an `http.Handler`, which destroys escape hatch 2 (§6.2)
because a user can no longer call `.Handle`; mint inside each generated handler,
which leaves every custom route without an identifier and contradicts "every
response" above; or hand the user both pieces. lapigo generates both —

```go
mux := httpapi.NewRouter(store, hooks)   // escape hatch 2: still a *http.ServeMux
mux.Handle("GET /healthz", healthz)      // still works
srv := &http.Server{Handler: httpapi.WithRequestID(mux), /* … */}
```

— and the scaffolded `cmd/api/main.go` (§6.1) writes that composition, so it is
correct out of the box and custom routes registered on the mux are inside the
middleware, not outside it.

The cost, stated: this is the one guarantee in §6.9 that a user can silently
lose. `main.go` is scaffolded once and never touched again (§6.1), so a user who
removes the `WithRequestID` wrapper, or serves the bare mux, gets responses with
no `X-Request-Id`; `respondError` falls back to minting one so the envelope
member is never missing, but a fallback identifier appears in no log line
written earlier in that request and therefore correlates nothing. The alternative was surrendering the escape hatch that four
sections depend on, which is worse.

**16 bytes from `crypto/rand`, hex-encoded** — 32 characters. Not
UUID-formatted: the hyphenated shape is read by tooling and by people as a UUID,
and a UUID has version and variant semantics this value does not have. Something
that is not a v4 UUID should not look like one.

**An inbound `X-Request-Id` is adopted only if it matches 1–128 characters of
`[A-Za-z0-9_-]`**; anything else is discarded and a fresh identifier minted, with
no error to the client. Adoption is what lets a request keep its identity across
a proxy or an upstream service, and the pattern is what stops the header being a
log-injection vector: the value reaches a log line, and a client-supplied `\n`
in a log line forges log entries. This is generated code shipped to every user
of lapigo, so an unvalidated string reaching a log line is not one project's bug.

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

The key is defined in §3.5. The emitted set is deduplicated by column list —
a filter column that is also a sort key collapses into the unfiltered sort's
index, since the duplicated column is constant under the equality seek and
contributes no pathkey — and a single-column list equal to the primary key's
emits nothing, because the primary key constraint already created exactly
that index and a second copy would double every insert's index writes for no
read benefit.

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

A cursor's member keys are column names, like every other name on the wire
(§6.9.2).

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
- **DDL emitter** — golden `.sql` files under `internal/ddl/testdata/`,
  asserted byte for byte with the same `-update` convention; emitted SQL is
  output users run against a real database, so a regeneration is reviewed as
  a diff, never routine. Determinism (twenty runs, byte equality) and
  Postgres's 63-byte identifier limit are asserted on every fixture's real
  output. Every golden fixture is also **applied to a real Postgres**
  (`LAPIGO_TEST_DATABASE_URL`, the variable CI already provisions): rendering
  cleanly is not the SQL analogue of compiling — applying is — so an emitter
  change that produces server-rejected SQL fails CI even when every
  byte-for-byte assertion still passes. Each fixture applies into its own
  fresh schema; the tier skips only outside CI — a skipped test prints
  nothing under `go test ./...` without `-v`, so in CI (where the variable
  should always be set) a missing `LAPIGO_TEST_DATABASE_URL` fails the build
  rather than leaving a green pipeline that proves nothing, while
  `make check` on a machine with no Postgres stays green.
- **Templates** — golden files under `testdata/<case>/` with the `-update` flag
  convention, so regeneration is reviewed as a diff.
- **Declared names equal rendered names.** The fixture matrix of §5.6 is
  rendered, its output parsed with `go/ast`, and the set of top-level
  declarations per package asserted **equal** — in both directions — to the set
  §5.6's table predicts. A subset assertion in either direction leaves one drift
  direction unchecked. A second test asserts the matrix itself covers each
  declaration-changing option in both its present and absent states, so adding
  an option without extending the matrix fails instead of quietly narrowing what
  the equality test can see.
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
- **The wire contract (§6.9)**, asserted on whole response bytes, not on
  substrings: an empty list page is `{"items":[],"next":null}` and never
  `"items":null`; a nullable field holding NULL is present as `null` and does not
  vanish; `Location` after a create with a `/`-bearing primary key is escaped;
  a repeated, empty, unknown, or unparseable query parameter is `400` with the
  parameter named in `fields`; an oversized body is `413` and not `400`;
  `application/json; charset=utf-8` is accepted and `text/plain` is `415`; an
  inbound `X-Request-Id` containing a newline is discarded rather than echoed.
- **Optimistic concurrency (§6.6)**, against a real Postgres: a stale `If-Match`
  is `409` and leaves the row unchanged; `*` succeeds; a weak tag is `400`;
  create yields version 1 and an `ETag` matching it.

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
5. **Resolved 2026-08-22 — the entity-level `indexes:` key is specified in
   §3.5.** Step 4 had to settle this before emitting an index set, and
   specified it rather than dropping it from §7.2: the escape hatch is what
   keeps the *N+1* bound honest for schemas with a hot combined-filter query,
   and the reasoning that removed revision 1's `index: true` — a user-asserted
   claim about a database lapigo neither created nor inspected — does not
   apply now that DDL generation is phase 1: lapigo creates exactly what the
   key declares.
6. **Resolved 2026-09-10 — the version column is constrained in §3.6.** The
   nullable case this question raised was one of four ways to declare a version
   column the store cannot work with; the type and the primary-key overlap were
   found alongside it, and all four are settled together by validator rules
   rather than by NULL-version update semantics. §6.6 states the ETag and
   `If-Match` grammar that depends on them, and §6.5's table carries the input
   exclusion.

---

## 10. Build order

| # | Step | Package | State |
|---|---|---|---|
| 1 | `Pos`, `At[T]`, `Diagnostic`, rendering, tab rejection | `internal/source`, `internal/diag` | done — `bba8255` |
| 2 | Parser: YAML → AST → IR with positions | `internal/ir`, `internal/parse` | done — `37893df` |
| 3 | Validator: §3.1, §3.3, §3.4, §5.6 | `internal/validate` | done — `a87b00d` |
| 4 | DDL emitter: `CREATE TABLE`, constraints, indexes from §7.2 | `internal/ddl` | done |
| 5 | Template engine, formatting, determinism, staging, lock | `internal/gen` | not started |
| 6 | Hooks interfaces and no-op implementations, plus `model`'s `Optional[T]` and the `CreateInput`/`UpdateInput` struct types (§6.5) their signatures require | `internal/gen` | not started |
| 7 | Input validation (`Validate() error`, §6.5), store, cursor encoding, keyset predicate | `internal/gen` | not started |
| 8 | Handlers, error envelope, router, resource bounds | `internal/gen` | not started |
| 9 | `lapigo new`, `lapigo gen` | `cmd/lapigo` | not started |

The dependency order is real and revision 1 got it wrong by claiming steps 5–7
could run in parallel: the store invokes hooks inside its transaction, so hooks
precede the store, and handlers depend on the store's types and cursor
encoding. **7 depends on 6; 8 depends on 7.** Only 4 is genuinely independent of
5–8 once 3 is in place.

**Step 6's own row was wrong until measured, and is corrected here rather
than left for step 7 to discover a second time.** §6.3's hook signatures name
`*model.<Entity>CreateInput` and `*model.<Entity>UpdateInput` directly — they
are not decoration, they are Go identifiers the compiler resolves — so those
two struct types are hooks' *prerequisite*, not step 7's consequence. A step
whose own package cannot type-check is not a step boundary. Step 6 therefore
carries `model.Optional[T]` (§6.5, in full: all three states and all six
methods) and the `CreateInput`/`UpdateInput` struct *types* — membership and
member type only, via `Field.CreateInputPresence`/`UpdateInputPresence`/
`ValueGoType` (§2.2's issue #25 accessors). `Validate() error`, the method
that actually enforces §6.5's "Question 2" (mandatoriness), stays step 7's:
nothing about hooks needs it, and it is the first method §5.6's table
predicts on a covered receiver for `internal/validate`'s `reservedMethodNames`
containment check (§5.6's own "what this does not yet check").

**Step 5 carries a debt from step 3.** `internal/validate`'s
`reservedMethodNames` rejects a field whose Go name would collide with a method
the templates generate (§5.6). That list was asserted from this specification
because the templates it names did not exist yet, and nothing links the two: a
method added to a template without a matching entry there produces a schema
that validates clean and fails to compile only once regenerated. Per CLAUDE.md
an invariant that matters is checked by something, not asserted in a comment —
so the first commit of step 5 must close it.

**The mechanism this document used to state for that is not achievable, and
§5.6 now states the one that is.** "Derive the reserved set from the templates"
cannot be done from template text: `text/template` is not parseable Go, and the
identifier a template emits is a function of runtime data. The derivation runs
in the other direction — render §5.6's fixture matrix, parse the *output* with
`go/ast`, and assert set equality per package in both directions (§5.6, §8).
The fixture matrix is part of the same commit, because a matrix that omits an
option hides every declaration that option controls.

`reservedMethodNames` is a list of **method** names, so the equality test has to
see methods or it closes nothing: it compares every top-level declaration
`go/ast` reports, an `*ast.FuncDecl` with a receiver included, and §5.6's table
predicts them. The collision set spans four packages; `store`'s share lands with
step 7 and `httpapi`'s with step 8, and each commit fills in its own rows rather
than leaving the table to drift until someone notices.

---

## 11. What must be true before phase 1 is done

- `lapigo new demo && lapigo gen && psql -f migrations/0001_init.sql && go run ./cmd/api`
  serves a working CRUD API — **with no `go mod tidy` and no network access**,
  which is what §6.1's pinned `go.mod` requires and its embedded `go.sum` makes
  possible.
- Paginating a 500 000 row table reads a constant number of buffers per page,
  independent of depth, proven by the §8.1 assertion.
- No generated SQL contains an interpolated identifier or value.
- Every error response is the §6.7 envelope — `request_id` included — and no
  `500` body contains a driver message.
- Every success response matches §6.9: `items` is never `null`, `next` is always
  present, and no two implementers of that section could differ in a byte.

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
| `FormatOnly: true` dropped | Milliseconds of build time bought with a permanent correctness risk and no correction pass. **Reversed on 2026-09-10 — all three premises were wrong; see §5.2 and §13.** |
| Tabs rejected in the schema | goccy under-counts tab columns; the test revision 1 promised would have failed against the real library. |
| Cursor: entity added to the fingerprint, canonical form specified, UTC normalisation, no lexical comparison | Cross-entity replay; nondeterministic fingerprints; cursors unstable across hosts because pgx scans into `time.Local`. |
| HMAC deferred to 1.5 | Key storage, key identifier and rotation were unspecified while the server was required to fail closed without a key. |
| Relation expansion deferred to 1.5 | Loading strategy was unspecified, and N+1 avoidance is the project's stated core value. |
| Staging directory dot-prefixed; atomicity claim withdrawn | A plain sibling directory is compiled by `go build ./...`. Two renames are not atomic; the recovery path is stated instead. |
| `go/types` test tier merged into `go build` | "No subprocess" was false once generated code imports pgx. |
| Build order corrected | Hooks precede the store, which precedes the handlers. |

---

## 13. Amendments after acceptance

Revision 2 was accepted as written; these changes were made to it afterwards,
each because implementing against it exposed something it had not settled.
A change here is a change to the contract — record it, do not make it silently.

| Date | Section | Change | Cause |
|---|---|---|---|
| 2026-08-19 | §2.2 | `Entity` gained `NameSpan` and `TableSpan`, `Filter` gained `Span`, `Relation` gained `NameSpan` and `TargetSpan`, and `SortKey.Pos` became `SortKey.Span` | Building the validator showed the IR could not blame the right token: a diagnostic about a filter pointed at the filtered field's declaration rather than at the offending `filters:` entry. Only the parser sees the written form, so only it can record an exact width. |
| 2026-08-19 | §2.2 | Every `Relation` that exists carries a valid `TargetSpan` — stated as an invariant `Schema.Freeze` checks, not as prose | A first attempt claimed the opposite (a zero `TargetSpan` when `target:` was omitted). An adversarial review disproved it by enumerating all four target forms: no `Relation` is ever appended whose target went unwritten. |
| 2026-08-19 | §2.2, §3.3 | Omitting `sort:` synthesizes `[-<pk>]`; writing `sort: []` is a parse error | CLAUDE.md decision 4 requires that a sort with no unique tiebreaker not be expressible. Omitting `sort:` produced an entity with zero sort keys and zero diagnostics, so it was. |
| 2026-08-22 | §3.5 (new), §9.5 | Entity options specified: `table:` formalized, `indexes:` defined with its validator rules; open question 5 resolved by specifying rather than dropping | Building the DDL emitter (step 4) had to settle §7.2's promised-but-undefined escape hatch before emitting an index set. The reasoning that removed `index: true` does not apply now that DDL is phase 1: lapigo creates what the key declares. |
| 2026-08-22 | §7.2 | The emitted index set deduplicates by column list, and a single-column list equal to the primary key's emits nothing | Writing the emitter surfaced both as duplicate-index emissions: a filter column that is also a sort key adds a constant column, and the pkey constraint already indexes its own column. |
| 2026-08-22 | step 4 | Foreign keys emit as `ALTER TABLE` after every `CREATE TABLE`; every table and column identifier is emitted double-quoted (the pg_dump convention); derived constraint and index names follow Postgres conventions (`_pkey`, `_key`, `_check`, `_fkey`, `_idx`), kept within 63 bytes by deterministic truncation plus a hash suffix; `default: now` emits `DEFAULT CURRENT_TIMESTAMP`, literals emit as single-quoted SQL strings, `default: uuid` emits nothing | A relation cycle (nullable mutual FKs) is representable and resolves — only PK-belongsTo cycles are parse errors — so no CREATE TABLE order can carry inline REFERENCES. The parser's identifier grammar accepts Postgres reserved words (`order`, `select`, `user` are legal field names), and an unquoted reserved word in a table or column position is a syntax error at apply time — quoting everything makes the bug unrepresentable rather than maintained against a keyword list that drifts with Postgres versions. Postgres silently truncates identifiers past 63 bytes, colliding prefix-sharing names. The DEFAULT rules pin what §3.2 left implicit; uuid stays client-side per its own rationale. |
| 2026-08-22 | §3.1 (parse rules) | Identifiers are rejected above Postgres's 63-byte limit; enum values must be non-empty and free of control characters | Postgres would silently truncate a longer identifier, leaving generated code and database disagreeing; a control character in an enum value cannot be emitted into the migration's CHECK literal (Postgres rejects NUL outright). |
| 2026-08-22 | §3.1 (validation) | `on_delete: set_null` is rejected on a relation that is not nullable | The DDL emits the clause as asked; NOT NULL plus ON DELETE SET NULL applies cleanly and then fails on every delete of a referenced row — a runtime 500 generated from a schema that validated clean. |
| 2026-08-22 | §8 | The DDL test tier applies every golden fixture to a real Postgres via `LAPIGO_TEST_DATABASE_URL`, each into a fresh schema; it skips only outside CI (an unset variable in CI fails the build, so a pipeline that has lost its database goes red instead of silently green); pgx v5 enters the generator's go.mod as a test-only dependency | Review of step 4 against a live database: CI has provisioned a Postgres and exported the variable since the first pipeline, and nothing in the repository read it — "applies cleanly" was established by hand once, and nothing re-established it. The CI guard answers the follow-up: a skip prints nothing without `-v`, so the tier must fail where a database was promised. pgx is the project's chosen driver (§2.3); executing psql instead would trade a declared module dependency for an undeclared environment one. |
| 2026-09-09 | §2.2 | `Field.EnumValues` is `[]EnumValue{Name, GoName}`, not `[]source.At[string]` | Issue #24: nothing computed an enum value's Go identifier, so `values: [in-progress, in_progress]` — two values that case-convert to the same generator constant — validated clean and would have produced uncompilable code. §5.6's collision check needs a real identifier per value to compare. |
| 2026-09-09 | §3.1 (parse rules) | An enum value whose computed Go identifier is empty (e.g. `"---"`, every character a separator) is rejected, alongside the existing non-empty/no-control-character rules | The enum-value counterpart of the field-name check below: `"---"` is legal enum-value text by the existing rules but names nothing a generated constant could carry. |
| 2026-09-09 | §5.6 | The package-wide collision check compares every generated top-level declaration together — entity structs, enum types, *and* one enum constant (`EnumGoType + EnumValue.GoName`) per value — not enum types and entity structs alone; a name already rejected by the identifier grammar (leading digit aside) is excluded from this comparison rather than compared using its Go name anyway | An enum constant's name concatenates two variable-length prefixes (`entityGoName + goName(fieldName) + goName(value)`), which is ambiguous: field `state` value `x_y` and field `state_x` value `y` both produce `TaskStateXY`. Checking only entity and enum-*type* names (as a first pass at this issue did) missed that, and every within-field, cross-field, type-vs-constant, and constant-vs-entity-struct pairing besides (PR #39 review finding F1). Comparing an already-invalid name's mangled Go form against a valid sibling's produced a second diagnostic blaming the valid one for the invalid one's own defect (PR #39 review finding F2); excluding it entirely is more useful than merely correcting the message, since the invalid name already has its own diagnostic. |
| 2026-09-10 | §2.2, §3.2, §6.5 | **Every** member of both input types is a generated `Optional[T]` implementing `json.Unmarshaler`, not pointers; the "a pointer distinguishes absent from explicit null" claim is removed from §3.2 and §6.5 and replaced by the measurement that disproves it; the model keeps the pointer form, restated as a *marshalling* rule | Issue #15: `encoding/json` sets a pointer to `nil` for the literal `null`, so `{}` and `{"body":null}` are indistinguishable after decoding, and `**T` fails the same way. §6.6 required semantics the prescribed mechanism could not produce. The false claim appeared in two places, which is why §3.2 now names the measurement instead of restating the conclusion. Review pinned three further details: mandatory members are `Optional[T]` too, because a bare `string` member decodes `{}` and `{"title":null}` to the same `""` with no error and could not be validated at all — the same trap one type further along; `Get`'s second result is `present && !null`, not `present`, so a `null` never hands a caller a zero value to write as data; and `Set`/`SetNull` exist because hooks have no JSON to decode and §6.4's derived fields would otherwise be create-only. |
| 2026-09-10 | §3.1, §3 example, §6.5 | `default:` means "supplied at insert when the request omits the field", not "never settable"; `readonly:` is the option that makes a field unsettable, and §3's `created_at` now carries it | Issue #16: with §6.5 building the update input by subtraction, `default:`'s create-time exclusion propagated, so `status: {default: draft}` produced a CRUD API in which status could never be changed. The two options were near-synonyms and the format had lost its commonest field shape — optional, with a fallback. Consequence for merged code: `internal/validate/sort.go`'s `\|\| f.Default != nil` exemption from the mutable-sort-key warning became wrong under this reading and was removed in #49. |
| 2026-09-10 | §3.1, §6.5 | Input projection is **two independent questions** — is the member there (a first-match table), and must the client send it (`required:` and no `default:`) — not one table and not a subtraction. A field that is `required:`, carries no `default:`, and is absent from `CreateInput` is a schema error. §3.1's option table no longer states any input projection | Issues #16 and #18: a subtraction has one knob per field and can only remove, never re-add or mark optional; it manufactured two of the three defects settled here. A single first-match table then manufactured a third, found in review: `{required: true, immutable: true}` matched the `immutable:` row and became *optional* on create, so omitting it inserted NULL into a NOT NULL column with no default — `23502`, which §6.7 does not map, hence a 500 on every create from a schema that validates clean. §3.1's `required` row ("and required in create input") was a second, drifting copy of the same rule and contradicted §6.5 eight lines below it. |
| 2026-09-10 | §6.9 (new), §2.2, §6.7 | The wire contract: list envelope `{"items":…,"next":…}` with `items` never null and `next` never `omitempty`; 201 + relative escaped `Location` on create (omitted when no `get` endpoint); 204 and a 404 for an absent row on delete; flat query parameters with `limit` and `after` reserved; per-type value parsing with every failure a 400; repeated, empty and unknown parameters rejected; JSON key = column name with explicit tags and no `omitempty`; `request_id` on every error plus `X-Request-Id` on every response; 413 `body_too_large`; `Content-Type` required on POST/PATCH | Issue #17: revision 2 defined the error envelope and nothing about success. The list body, the status codes, the parameter names, the filter syntax and the JSON key rule are the public API of every generated project, and they lived in `CLAUDE.md`, a French design note, or nowhere. `NaN`/`Inf` and an undeclared enum value both return `200` with zero rows if accepted, which is the worst available outcome; `?status=` cannot mean `IS NULL` because §7.3 bans the idiom that would implement it. `request_id` minted in `respondError` cannot correlate a log line written earlier in the same request, so it is minted by a middleware into the context — exported separately and wrapping the mux from outside, because a `ServeMux` cannot run anything ahead of its own dispatch and wrapping it inside the constructor would destroy escape hatch 2. |
| 2026-09-10 | §6.9.1 | The route path source is **ratified**: the collection path is `"/" + Entity.Table`, and an entity-level `path:` key is deferred to phase 2 as a decision rather than an oversight. The IR half of this — `Entity.Path`, the PK-named wildcard — is the #47 row below | Issue #23: step 2 invented the convention and `internal/parse/endpoint.go`'s own doc comment admitted the spec said nothing. `table:` is currently the only lever a schema has over its URLs, so deriving from `Entity.Name` would remove the one knob and add none; and both names go through the same `identifierPattern`, which forbids `-` and `/`, so `/blog-posts` is unreachable from either. |
| 2026-09-10 | §3.6 (new), §3.1, §6.5, §6.6, §9.6 | A `version:` column must be `int` or `bigint`, `required: true`, and not the primary key; it is excluded from both input types by its own §6.5 row — explicitly **not** by a synthesized `readonly:` flag; §6.6 gains the ETag/`If-Match` grammar, including `DELETE` on a versioned entity. Open question 6 resolved | Issue #18: measured against the merged parser, `id: {type: uuid, pk: true, version: true}`, `type: text` and `type: json` all validate clean, and step 7 would emit `SET version = version + 1` against each. A nullable version makes every comparison unknown, so `If-Match` returns 409 forever. A client that can PATCH its own version defeats `If-Match` entirely. The flag was rejected as the exclusion mechanism because `internal/validate/sort.go` exempts `ReadOnly` fields from the mutable-sort-key warning, and a version column is the worst possible sort key in the format. |
| 2026-09-10 | §5.2, §2.1, §2.3, §6.1, §11, §12 | `imports.Process` runs with `FormatOnly: true`; step 6's computed import set is authoritative and the Go compiler is the correction pass. `lapigo new` writes a `go.mod` with the pgx version pinned as a build-time constant plus pgx's indirect requires, and an embedded `go.sum`. This reverses §12's own reversal | Issue #19: measured, with resolution enabled, against a `go.mod` correctly requiring `pgx/v5 v5.10.0`, goimports added the **pre-v5** `github.com/jackc/pgx` from the machine's cache and exited 0; against an empty cache it dropped the pgx import entirely and exited 0. Same schema, same `go.mod`, different bytes per developer — a §5.3 violation no sorting fixes. §12's stated reason was "milliseconds bought with a permanent correctness risk and no correction pass": resolution costs 135–550 ms per file, **is** the correctness risk, and §8's `go build` tier is the correction pass, catching both `undefined: pgx` and `imported and not used`. Without the embedded `go.sum`, `go build` fails with `missing go.sum entry`; without the indirect requires, with `updates to go.mod needed`. |
| 2026-09-10 | §6.3, §6.4, §6.7 | The hook interface is stated in full for all five operations: `BeforeDelete` takes the id only, `AfterDelete`/`AfterDeleteCommitted` take the row from `DELETE … RETURNING`, `BeforeUpdate` takes no pre-image, and there are **no** `List`/`Get` hooks in phase 1. A `hooks.Error{Status, Code, Message, Fields}` carries a hook's own status into `respondError`, which matches it with `errors.As` and clamps a Status outside 400–599 to 500 | Issue #20: "same shape for Update and Delete" is not a shape anyone can copy — there is no `DeleteInput`. A pre-image for `BeforeDelete` or `BeforeUpdate` would force a `SELECT … FOR UPDATE` on every write of every entity, because the store cannot know at generation time whether a user's hook is still the no-op — the blanket-tax reasoning that rejected `COUNT(*)` and offset pagination. Read hooks have no transaction to receive, so their signature is an open question rather than a copy-paste, and the payoff people want from them is relation expansion, deferred to 1.5. Without the typed error a user's own validation rejection was emitted as a 500. The `Present()` mechanism replaces the pointer test an earlier analysis assumed (see the §6.5 row above). |
| 2026-09-10 | §5.4, §5.5, §6.1 | The lock carries two entry kinds: `generated` (`internal/gen/**`, checksum of the file as written, compared every run) and `emitted-once` (`migrations/0001_init.sql`, checksum of what lapigo **emitted**, never compared to disk). The migration warns when the schema has moved, stops when the file is gone, and is never rewritten. `Generate`'s map contains Go files only. §5.5 row 1 reads "present **and** differs", and the entry-present/file-absent rows are added, resolved by kind | Issue #21: the lock conflated "was this hand-edited?" — which protects a file about to be overwritten — with "is this behind the schema?", which is provenance and is answerable from the emitted checksum, since a hand edit does not move it and a schema change does. The migration is never overwritten, so the first question manufactures a hard stop on the state open question 4 already treats as normal. Read literally, "checksum differs" was true of a missing file, so `lapigo gen` demanded `--force` after `rm -rf internal/gen`. `ddl.Emit` is pure, total and deterministic, so re-emitting every run is free. Keeping the migration out of the map keeps every entry a Go file with no exception to carry through §5.2, §5.3 and §5.5. |
| 2026-09-10 | §5.6, §6.3, §10 | §5.6 enumerates the generated identifier set per package — `model`: `E`, `ECreateInput`, `EUpdateInput`, `EF`, `EF<Value>`; `store`: `EStore`, `EListQuery`; `hooks`: `EHooks`, `NoopEHooks`; `httpapi` with step 8 — compared **within** a package, attributed to the entity's `NameSpan`. §6.3's user-side example is corrected from one `gen` package to `hooks`/`model`. §10's "derive from the templates" is replaced by rendering a fixture matrix, parsing the output with `go/ast`, and asserting set equality per package in both directions | Issue #22: §5.6 promised "reject, never mangle" for a collision class it never enumerated, so entity `article` and entity `article_create_input` both yield `ArticleCreateInput` and validate clean. §6.1's four-package tree and §6.3's single-package example gave different collision domains and the tree is authoritative — cross-package comparison would reject a legal schema, which "reject, never mangle" does not license. `NoopEHooks` is a prefix form, so a known-suffix shortcut misses it. Deriving from template text is impossible: `text/template` is not parseable Go and the identifier depends on runtime data. Set equality rather than subset, because a subset in either direction leaves one drift direction unchecked; the fixture matrix needs each declaration-changing option present **and** absent. Review then found three ways the test could not have passed as first written: the per-entity table was unconditional while `endpoints:` is not, so a fixture omitting `create` failed direction 2 on `ECreateInput`; `store` had no fixed set and unexported helpers were outside the definition, so a store fixture failed direction 1; and the table predicted no methods, while `reservedMethodNames` — the debt this is meant to close — is a list of method names. |
| 2026-09-10 | §2.2, §6.5 | `Field` gained `ValueGoType() string` — the Go type with nullability stripped, the `T` of every input member's `Optional[T]` — beside `GoType()`, which keeps answering for the model; and §6.5 states that generated input validation is a `Validate() error` on each input type in `model`, returning a `model`-owned error, never `*hooks.Error` | Review of #15: `GoType()` returns `*string` for a nullable field and was the IR's only Go-type accessor, while §3.2 now says input types use no pointers — §5.1 forbids a template computing the difference, so this is the same omission class as the `Method()`/`SQL()`/`SortedFilters()`/`Has*()` accessors added for issue #25. And `hooks` imports `model` for every signature in §6.3, so a `model.Validate` returning `*hooks.Error` would have closed an import cycle; `httpapi` imports both and maps each to the envelope. |
| 2026-09-10 | §1 | The in-scope bullet no longer says generated files are "atomically written" | §5.4 withdrew that claim and §12 records the withdrawal; §1 was the last place still asserting it. |
| 2026-09-10 | §2.2 | `Entity`'s struct listing gained the `Indexes []Index` field it was already carrying in code (added by the 2026-08-22 §3.5 amendment above, but never reflected here); `EndpointKind` gained `Method() string`; `Entity` gained `HasList`/`HasGet`/`HasCreate`/`HasUpdate`/`HasDelete`; `Entity` gained `SortedFilters() []Filter`; `FilterOp` gained `SQL() string` | Issue #25: the templates step 5+ will consume were about to inline `"METHOD /path"` strings, per-kind boolean chains, an ad hoc sort for §7.4's fingerprint, and a hardcoded `"="`, each in template code — exactly what §5.1 forbids. `Entity.Filters` itself stays in declaration order; `internal/ddl/ddl.go`'s `indexColumnLists` derives one index per declared filter in that order, so the canonical by-name view §7.4 needs is a separate method, not a sort in place. |
| 2026-09-10 | §2.2 | `Endpoint.Path` is removed; `Entity` gained `Path(kind EndpointKind) string`, computed from `Kind`, `Table` and `PK.Column`. The single-resource wildcard is named after `PK.Column`, never hardcoded to `"{id}"` | Issue #47: `Path` was a stored field whose only inputs were `Kind`, `Entity.Table` and `Entity.PK.Column`, the exact shape `Field.GoType`/`PgType` already refuse for the same reason (§2.2's own field.go rationale). Worse, `buildEndpoints` runs during entity resolution, before a `belongsTo` PK's `Column` is finalised in `resolvePendingRelations`' fixed point, so a stored `Path` risked freezing a placeholder. A hardcoded `{id}` wildcard also bound a decoded path value to the wrong column on any entity whose PK was not literally named `id` — e.g. `slug`. |
| 2026-09-11 | §10, §5.6 | Step 6's row grows to include `model.Optional[T]` (all three states, all six methods) and the `CreateInput`/`UpdateInput` struct types — membership and member type only, not `Validate`. Step 7's row narrows to input validation, the store, the cursor codec and the keyset predicate. §5.6's table now attributes `ECreateInput`/`EUpdateInput` to step 6 and `ECreateInput.Validate`/`EUpdateInput.Validate` to step 7 | Issue #62, building step 6 (#27): §6.3's hook signatures name `*model.<Entity>CreateInput`/`UpdateInput` directly, and those two Go identifiers have to resolve for `internal/gen/compile_test.go`'s `go build ./...` to pass. Measured directly against the `full` and `no_list` fixtures (both declare `create`/`update`) with the hooks templates in place and nothing else changed: `full/internal/gen/hooks/article.go:22:57: undefined: model.ArticleCreateInput`, repeated for `ArticleUpdateInput`, `EventCreateInput` and `EventUpdateInput`. The old row order — "7 depends on 6" while 6 needs 7's own types to compile — was backwards, not merely incomplete: a step whose own package cannot type-check is not a step boundary, and `make check` is meant to be green at every one. Stubbing empty structs to force a green build instead was rejected: it would have `model`-package, `HasCreate()`-gated declarations originate from step 6's commit while §5.6 and `names.go` explicitly earmarked them for step 7's, and would ship a type with none of §6.5's actual content (no member carried the field it is named for). `Validate` stays step 7's because nothing about hooks needs it, and because it is still the first method §5.6's table predicts on a receiver `internal/validate`'s `reservedMethodNames` covers — step 6's hooks live on `<Entity>Hooks`/`Noop<Entity>Hooks`, receivers that carry no entity field, so §5.6's own containment debt (its "what this does not yet check") first becomes checkable in step 7, unmoved by this change. |
