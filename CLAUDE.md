# CLAUDE.md

Guidance for Claude Code (and any AI agent) working in this repository.

> Repository documentation is written in English because this is a community
> project with an international audience. Brainstorming notes under `docs/notes/`
> are in French and stay that way.

---

## What lapigo is

lapigo is a **code generator**. You describe your entities in a `lapigo.yaml`
file, and lapigo emits idiomatic, readable Go: handlers, data access, routing,
validation, migrations, and OpenAPI documentation. **The generated code belongs
to the user.** They can read it, debug it, step through it, and delete lapigo
from their toolchain without rewriting their application.

## What lapigo is not

- **Not a runtime.** There is no lapigo server, no lapigo process in
  production, no dynamic schema stored in a database. If a feature requires
  lapigo to be present at runtime, it does not belong here.
- **Not a framework.** Generated code imports the standard library and `pgx`.
  Nothing else. A user must never have to learn "the lapigo way" to read the
  code they were handed.
- **Not a fork of anything.** Written from scratch. We borrow ideas — PocketBase's
  filter syntax is well designed and worth studying — but not code.

---

## Project status

**Phase 1, in build. Steps 1-4 of the build order are merged.**

The architecture is settled (see below) and the phase 1 specification is
**accepted**: `docs/superpowers/specs/2026-08-15-phase1-core-design.md`,
revision 2. Read it before writing code. It is the contract the implementation
is held to, not a sketch, and it records in §12 why each decision was reversed
from revision 1 — so a change that reopens one of those needs an issue.

| Build order step (spec §10) | State |
|---|---|
| 1. `Pos`, `At[T]`, `Diagnostic`, rendering, tab rejection | done — `bba8255` |
| 2. Parser: YAML → AST → IR with positions | done — `37893df` |
| 3. Validator: §3.1, §3.3, §3.4, §5.6 | done — `a87b00d` |
| 4. DDL emitter: `CREATE TABLE`, constraints, indexes from §7.2 | done |
| 5. Template engine, formatting, determinism, staging, lock | **next** |
| 6. Hooks interfaces and no-op implementations | not started |
| 7. Model and input types, store, cursor encoding, keyset predicate | not started |
| 8. Handlers, error envelope, router, resource bounds | not started |
| 9. `lapigo new`, `lapigo gen` | not started |

What exists under `internal/`: `source`, `diag`, `ir`, `parse`, `validate`,
`ddl`. `internal/gen` does not exist yet, and neither does `cmd/lapigo` — which
is why `make check` runs `build-all` and not `build`.

The order in §10 is a real dependency order, not a preference: hooks precede
the store because the store invokes them inside their transaction, and the
handlers depend on the store's types and cursor encoding. Step 5 carries a
debt recorded in spec §10: the first commit of step 5 must derive
`internal/validate`'s `reservedMethodNames` from the templates it generates,
not from this document's text. Do not start a step whose predecessor is not
merged.

---

## Non-negotiable architecture decisions

These were settled deliberately. Do not relitigate them in a pull request; open
an issue instead.

### 1. YAML in, Go out — through an intermediate representation

```
lapigo.yaml → Parse → AST → Resolve → IR → Validate → Render → Format → Write
```

**Templates never see raw YAML.** They only consume the IR: a validated Go
structure where every default is explicit and every relation is resolved.

This boundary is what keeps the project extensible. A new *input* (introspecting
an existing Postgres schema) produces the same IR and touches no template. A new
*output* (a TypeScript client, GraphQL) consumes the same IR and touches no
parser. Code that makes the YAML talk directly to a template will be rejected.

### 2. Strict generated/handwritten separation

`internal/gen/` is **rewritten in full on every run**. It is never edited by
hand. Users extend behaviour through explicit hooks, custom routes, and the
primitives the generated store exposes — never by editing generated files.

A `.lapigo.lock` manifest stores a checksum per generated file. If a file was
modified by hand, `lapigo gen` **stops and reports it** rather than silently
overwriting someone's work. `--force` overrides.

There are no user-overridable templates. Making templates a public extension
point would turn every lapigo release into a breaking change.

### 3. Standard library only in generated code

`net/http` with Go 1.22+ routing patterns, `pgx` for Postgres, nothing else. No
router framework, no ORM, no dependency injection container. This is the
project's core promise: the code you get has no exotic dependencies.

### 4. Cursor pagination only — no offset, no COUNT

Generated list endpoints accept `?limit=` and `?after=<cursor>` and return a
`next` cursor. They do **not** expose page numbers, totals, or `COUNT(*)`.

Three reasons, in order of importance:

1. **Correctness.** An offset-paginated cache entry becomes silently wrong the
   moment a row is inserted. A cursor-keyed entry can only be *missing*, never
   wrong, because the cursor is an absolute position in the sort order.
2. **Deep pages are free.** `OFFSET 60000` scans 60,000 rows. A keyset seek on
   page 3000 costs the same as page 1.
3. **No COUNT.** Aggregate counts force a scan. PocketBase measured 3.8–4.6s
   with count versus 9–110ms without, on 50k records with relations.

**Every sort must end with a unique tiebreaker column.** Without one, a keyset
scan skips or duplicates rows whenever two records share a sort value. The
validator rejects sorts that lack one — this must not be possible to express.

### 5. Deterministic output

The same `lapigo.yaml` must produce byte-identical files. Go randomises map
iteration order, so anything derived from a map must be explicitly sorted before
rendering. Non-deterministic generation produces noisy git diffs and makes the
tool unusable in a team.

### 6. Compiler-grade error messages

Validation errors cite a line, a column, and an actionable fix:

```
lapigo.yaml:12:12: sort key "created_at" is not unique
   12 |     sort: [-created_at]
      |            ^^^^^^^^^^^
   the last sort key must be unique; add a second key such as `-id`,
   or mark `created_at` unique
```

A generator that emits a Go stack trace on malformed input loses its user on the
first attempt. Position information is preserved from parsing onward for this
reason.

Columns are counted in **runes**, not bytes, everywhere. A caret computed from a
byte offset lands in the wrong place on any line containing a multi-byte
character, and an accented identifier is enough to trigger it. `internal/diag`
owns this rendering and its output is a public contract — tests assert on the
exact string.

### 7. Redis is optional

The cache is a phase 5 concern and generated code must be correct and fast
**without it**. Cursor pagination and the absence of COUNT are what carry the
load; the cache absorbs spikes and expensive filters. Never write generated code
that requires Redis to be present.

---

## Security

This is a community project. People will run generated code in production. A
security defect in a template is a security defect in every project that ever
used it. Treat template code with the seriousness of a library, not a scaffold.

### Rules for generated code

- **All SQL is parameterised.** No string concatenation of user input into a
  query, ever, under any circumstance.
- **Identifiers come from a whitelist.** Column and table names in sort and
  filter clauses are resolved from the IR, never from request parameters.
  A request may select *which* whitelisted column, never *name* one.
- **`limit` is capped** by the generator. An uncapped limit is a denial of
  service.
- **No cache on authenticated routes by default.** Caching a response that
  depends on identity or role without the identity in the cache key leaks one
  user's data to another. This is the single most dangerous bug in the design;
  it must be opt-in and explicit.
- **No mass assignment.** Generated input structs list their fields explicitly.
  A client must never be able to set a field the schema did not expose.
- **Errors do not leak internals.** Database errors, driver messages, and
  file paths never reach an HTTP response body. Log the detail, return a code.
- **Cursors are opaque and validated.** A cursor is decoded defensively; a
  malformed or forged cursor produces a 400, never a panic and never an
  unbounded scan.

### Rules for this repository

- **Never commit credentials, tokens, connection strings, or `.env` files.**
- **Never commit personal data** — real names, email addresses, or any context
  belonging to a maintainer's other projects. Contributors' git identities are
  their own business; the project's own commits use a GitHub `noreply` address.
- **Dependencies are a liability.** Adding one to the generator needs
  justification in the pull request. Adding one to *generated* code needs an
  issue and a maintainer's agreement first.
- Report vulnerabilities per `SECURITY.md`. Do not open a public issue for one.

---

## Git workflow

- `main` — stable. Never committed to directly.
- `develop` — integration branch. **All work merges here, through a pull
  request.**
- Feature branches: `feat/<short-name>`, `fix/<short-name>`,
  `docs/<short-name>`, branched from `develop`.

Pull requests must state what changed and why. A PR that changes generated
output must show a before/after sample of the generated code.

---

## Go conventions

- Format with `gofmt`; imports with `goimports`. Non-negotiable.
- `go vet` and `staticcheck` clean before opening a PR.
- Errors are wrapped with context: `fmt.Errorf("parse %s: %w", path, err)`.
- No `panic` in library code. The CLI may exit; packages return errors.
- Exported identifiers are documented. Unexported ones are documented when the
  reason for their existence is not obvious from the name.
- Prefer small, focused files. A file that has grown large is usually doing more
  than one job.

---

## Testing

**Test-driven.** Write the failing test first, watch it fail, then implement.

- **Parser and validator:** table-driven tests over fixture YAML files, covering
  the error cases as thoroughly as the success ones. Error messages are part of
  the contract — assert on them.
- **Templates:** golden-file tests. A fixture `lapigo.yaml` renders to a
  committed expected output. Regenerate deliberately, review the diff.
- **Generated code must compile.** The golden output is compiled as part of CI.
  Generated code that type-checks is the minimum bar.
- **Determinism:** generate twice, assert byte equality.
- **Data access:** integration tests against a real Postgres. Keyset pagination
  correctness — no skips, no duplicates under concurrent inserts — is tested
  explicitly, because that is where the subtle bugs live.

Never claim a test passes without having run it and read the output.

Two mechanical traps that have already cost us:

- **Use `go test -count=1`.** Go caches test results by package content. A cached
  `ok` after a change you made in another package looks exactly like a real pass.
- **Run `make check`, and check that CI is green before merging.** `make check`
  runs what CI runs, in the same order. Local verification that skips a CI step
  is how a red pipeline reaches `develop` unnoticed.

### Assert on exact values, never on substrings

This rule is here because ignoring it already cost us. An adversarial review
mutation-tested the first two packages: **16 of 36 deliberately introduced bugs
survived the suite**. Deleting the tab-handling that a function existed for
failed nothing. Replacing a caret-count clamp with `1 << 20` — a million carets
on one line — failed nothing.

The cause was uniform. Assertions like:

```go
if !strings.Contains(got, "lapigo.yaml:2:1:") { ... }   // passes on almost anything
if reason == "" { ... }                                  // says nothing about the message
if d.EndColumn != d.Pos.Column+1 { ... }                 // relational: passes if both are wrong
```

The handful of tests that pinned a full expected string caught nearly every
mutant. So:

- Compare the **whole** output to a literal, especially where the output is a
  contract — diagnostics, generated code, SQL.
- Never assert a value against another field of the same struct. That passes
  when both are wrong together.
- An `error` or reason string is part of the contract. Assert its exact text.
- A `String()` on an out-of-range enum must return something obviously bogus
  (`FieldType(99)`), never a plausible neighbour, and the test must say which.
- Do not write tests that assert Go's own semantics. `f := &Field{}; x := S{F: f}`
  then `x.F == f` tests the language, not lapigo — and it passes on the day the
  real invariant breaks.

**A test that cannot fail on the bug it exists to catch is worse than no test,
because it is believed.**

### Invariants live in code, not in comments

Same review, same lesson from the other direction: the documentation asserted
guarantees the types did not enforce. A doc comment saying a slice "is sorted by
name" with nothing sorting it, or "at most one per entity" with nothing
counting, is a wish.

If an invariant matters, something must check it — a constructor that is the only
way to build the value, or an explicit `Freeze()`/`Validate()` step with a test
that violates the invariant and expects the failure.

---

## Working with subagents

Implementation work in this repository is delegated to subagents by an
orchestrating session.

- **Implementation** runs on Sonnet, in an **isolated git worktree**, so
  parallel agents cannot collide.
- **Review** runs on Opus, on a **different agent than the one that wrote the
  code**, and is **adversarial**: the reviewer's job is to refute the work, not
  to bless it. Opus already self-challenges; asking it to re-verify repeatedly
  wastes tokens without improving the result.
- Each subagent gets one precise task and ends with it.
- Subagents should use the available skills rather than improvising an approach.

---

## Things not to do

- Do not add a runtime component.
- Do not add a dependency to generated code without agreement.
- Do not make templates user-overridable.
- Do not introduce offset pagination or a `COUNT(*)` "for convenience".
- Do not let a template read raw YAML.
- Do not sort by a non-unique key without a tiebreaker.
- Do not cache an authenticated response without identity in the key.
- Do not claim work is complete without running the verification and reading the
  output.
