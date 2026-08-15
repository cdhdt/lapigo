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

**Design phase. No implementation code exists yet.** The architecture is settled
(see below), the phase 1 specification is still being written. Do not start
implementing before a spec exists in `docs/superpowers/specs/` and the
maintainer has approved it.

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
