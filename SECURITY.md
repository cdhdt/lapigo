# Security Policy

## Why this matters more than usual here

lapigo is a code generator. A vulnerability in a template is not a bug in one
application — it is a bug reproduced in **every project ever generated with that
version**, including projects whose authors have long stopped following this
repository.

Treat template code with the seriousness of a security library.

## Reporting a vulnerability

**Do not open a public issue.**

Use GitHub's private vulnerability reporting: go to the **Security** tab of this
repository and choose **Report a vulnerability**. This creates a private
advisory visible only to maintainers.

Please include:

- what an attacker can do, and what they need to do it;
- a minimal `lapigo.yaml` that reproduces the problem, plus the generated output
  if relevant;
- the lapigo version or commit.

You will get an acknowledgement within 7 days and an assessment within 14. If a
fix is warranted, we will agree a disclosure timeline with you and credit you in
the advisory unless you prefer otherwise.

## Scope

**In scope**

- Any generated code that is exploitable: SQL injection, injection through sort
  or filter parameters, mass assignment, authentication or authorisation bypass,
  cache poisoning or cross-user cache leakage, denial of service through
  uncapped queries, information disclosure through error responses.
- Any flaw in the generator that lets a crafted `lapigo.yaml` execute code or
  write files outside the project directory.
- Supply-chain issues in this repository: compromised dependencies, malicious
  build steps, workflow token misuse.

**Out of scope**

- Vulnerabilities in an application's own handwritten code, hooks, or
  configuration.
- Deployment mistakes in a user's infrastructure.
- Missing hardening that the documentation explicitly describes as the user's
  responsibility.

## Classes of defect we consider critical by default

These are the failure modes the design is specifically built to prevent. A
report showing that generated code does any of the following will be treated as
critical:

- Interpolating request data into SQL rather than parameterising it.
- Accepting a column or table identifier from a request instead of resolving it
  from the schema whitelist.
- Serving a cached response that depends on identity or role without the
  identity in the cache key.
- Returning a database or driver error message in an HTTP response body.
- Allowing an unbounded result set or an unbounded scan from a crafted cursor or
  limit.

## Supported versions

The project is pre-release. Only the tip of `develop` is supported until a
tagged release exists.
