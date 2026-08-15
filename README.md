# lapigo

**Generate idiomatic Go CRUD APIs from a YAML schema.**

> **Status: design phase.** The architecture is settled and documented; no
> implementation exists yet. Watch the repository if you want to follow along,
> but do not expect working code today.

---

## The idea

You describe your entities once:

```yaml
entities:
  article:
    fields:
      title:   { type: string, required: true, max: 200 }
      body:    { type: text }
      status:  { type: enum, values: [draft, published] }
      author:  { type: belongsTo, target: user }
    sort: [-created_at, id]
    filters: [status, author]
    endpoints: [list, get, create, update, delete]
```

lapigo generates the handlers, the data access layer, the routing, the
validation, the migrations, and the OpenAPI document — as readable Go you own
and can edit around.

## What makes it different

**The generated code has no exotic dependencies.** `net/http` with Go 1.22+
routing, `pgx` for Postgres, and the standard library. No framework, no ORM. You
can delete lapigo from your toolchain tomorrow and your application still
builds.

**Cursor pagination, by construction.** List endpoints take `?limit=` and
`?after=<cursor>`. There is no offset and no `COUNT(*)` — so deep pages cost the
same as the first one, and a paginated read can never skip or duplicate a row
while someone else is writing.

**Fast on a small machine.** The target is several thousand requests per minute
on 4 vCPU and 8 GB of RAM. Not because of caching, but because the generated
queries are keyset seeks against indexed columns, and the whole class of
accidental N+1 queries is eliminated by construction — a template cannot write
one by inattention.

**Optional caching.** Redis support is planned as an opt-in layer with
range-based invalidation: writing one row invalidates the one cached page whose
key range contains it, not the whole collection. Generated code runs correctly
without Redis.

## How it compares

lapigo is not a competitor to [PocketBase](https://pocketbase.io), which is
excellent at what it does — a single binary with an admin UI, realtime, and file
storage, ideal for getting something running in five minutes. It is a *runtime*
with an embedded SQLite database and vertical-only scaling.

lapigo is a *generator*: no process in production, Postgres, stateless
application code, and cursor pagination. Different tool, different problem.

## Roadmap

| Phase | Scope |
|-------|-------|
| 1 | Core: YAML parser, IR, template engine, CRUD, cursor pagination, filters, sorting |
| 2 | SQL migrations generated from the schema |
| 3 | JWT authentication and per-endpoint role permissions |
| 4 | OpenAPI document generation |
| 5 | Optional Redis caching with range-based invalidation |

## Documentation

- [`CLAUDE.md`](CLAUDE.md) — architecture decisions and contribution rules
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — how to contribute
- [`SECURITY.md`](SECURITY.md) — reporting a vulnerability
- `docs/notes/` — design notes (in French)

## Licence

MIT. See [`LICENSE`](LICENSE).
