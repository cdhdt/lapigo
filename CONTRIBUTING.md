# Contributing to lapigo

Thanks for considering it. This project is in its design phase, so the most
valuable contributions right now are arguments, not patches.

## Before you write code

Read [`CLAUDE.md`](CLAUDE.md). It documents the architecture decisions and the
reasoning behind them. Several of those decisions look restrictive on purpose —
cursor-only pagination, no user-overridable templates, no dependencies in
generated code. They were settled deliberately.

**If you disagree with one, open an issue and make the case.** A pull request
that quietly reverses a documented decision will be closed, and that is a waste
of your time rather than a judgement on your idea.

## Workflow

1. Branch from `develop`: `feat/<short-name>`, `fix/<short-name>`, or
   `docs/<short-name>`.
2. Write the failing test first. This project is test-driven, and the parser's
   error messages are part of its public contract — assert on their **exact**
   text. `CLAUDE.md`'s testing section explains why substring assertions get
   rejected here, with the numbers from the review that taught us.
3. Keep `gofmt`, `go vet`, and `staticcheck` clean.
4. Open a pull request **against `develop`**, never against `main`.

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
