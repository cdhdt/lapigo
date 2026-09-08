# Contributing to lapigo

Thanks for considering it. Phase 1 is in build: the parser, the intermediate
representation and the validator are merged, and the generator is being written
against an accepted specification. Patches are welcome.

## Before you write code

Read [`CLAUDE.md`](CLAUDE.md) for the architecture decisions and the reasoning
behind them, and then read the specification the current work implements:
[`docs/superpowers/specs/2026-08-15-phase1-core-design.md`](docs/superpowers/specs/2026-08-15-phase1-core-design.md).
It is detailed on purpose — a contract, not a sketch — and its §10 says which
build order step is next and why the order cannot be reshuffled.

Everything binding on you as a contributor is in this file. If you want the
long form — the cycle a task goes through, what a reviewer owes you, and the
incidents each rule came from — it lives in
[cdhdt/dev-process](https://github.com/cdhdt/dev-process), shared across
projects. Where it disagrees with this file, **this file wins**: it is closer
to the code and it is what a reviewer will apply.

Several of the architecture decisions look restrictive on purpose — cursor-only
pagination, no user-overridable templates, no dependencies in generated code.
They were settled deliberately.

**If you disagree with one, open an issue and make the case.** A pull request
that quietly reverses a documented decision will be closed, and that is a waste
of your time rather than a judgement on your idea.

## Workflow

1. **Start from an issue.** No branch without a ticket — the pull request
   closes it with `Closes #N`, and that only works if it exists.
2. **Work in a dedicated worktree**, not a `git checkout` in a shared clone:

   ```bash
   git fetch origin
   git worktree add ../lapigo-<subject>-<N> -b <type>/<subject>-<N> origin/develop
   ```

   Branch types: `feat`, `fix`, `chore`, `ci`, `docs`, `refactor`, `test`.
3. **Write the failing test first.** This project is test-driven, and the
   parser's error messages are part of its public contract — assert on their
   **exact** text. `CLAUDE.md`'s testing section explains why substring
   assertions get rejected here, with the numbers from the review that taught
   us.
4. **Open the pull request as a draft at your first commit**, against
   `develop`, never against `main`. That is what makes work in progress
   visible; without it two people take the same ticket.
5. Keep `gofmt`, `go vet`, and `staticcheck` clean — `make check` runs exactly
   what CI runs, in the same order.
6. Mark it ready for review when CI is green. Review is adversarial and never
   by the author: the reviewer's job is to refute the change, and approval is
   what is left when that failed.

A pull request should say what changed and why. If it changes generated output,
include a before/after sample of the generated Go — reviewers need to see what
users will actually receive.

## Adding a dependency

Dependencies are a liability, and doubly so in a generator.

- **In the generator itself:** justify it in the pull request. Prefer the
  standard library.
- **In generated code:** open an issue first and get a maintainer's agreement.
  Generated code depends on the standard library and `pgx`. That list is
  short by design and changing it changes the project's central promise.

## Security

Do not report a vulnerability in a public issue or pull request. Follow
[`SECURITY.md`](SECURITY.md).

Never commit credentials, tokens, connection strings, `.env` files, or personal
data. If you accidentally push a secret, tell a maintainer immediately —
rotating it matters far more than the embarrassment, and rewriting history alone
does not make an exposed secret safe.

## Code of conduct

Be decent to each other. Critique the code, not the person. Maintainers will
remove comments and contributors that make this an unpleasant place to work.
