# CLI Rename Proposal: `cloudcompose` → a Shorter Binary, `env`/`compose` Subcommands

## Status: proposal, independent of `docs/multi-user-state.md`

This is a UX-motivated proposal, not a correctness fix — it doesn't
depend on, and isn't depended on by, the remote-backend/state-locking
work in `docs/multi-user-state.md`. It's written up separately so it
can be decided, scheduled, or dropped on its own timeline, and so it
never blocks that other, safety-relevant work.

## Motivation

`cloudcompose` requires typing the full compound word for every
invocation (`cloudcompose up`, `cloudcompose compile -f ... -e ...`).
For a tool meant to be typed constantly, that's a real ergonomic cost,
and it's also backwards from Docker Compose's own shape: `docker` is
the short, memorized top-level noun; `compose` is one of several
verbs/groups under it (`docker compose`, `docker build`, `docker run`).
Restructuring around a short top-level name with `env`/`compose` as
subcommand groups mirrors that shape and gives Docker Compose users a
CLI that already feels familiar.

## Open question: the name itself

The obvious choice, "cloud", has **not** been checked for availability
and shouldn't be treated as settled. "cloud" is a generic enough word
that it's likely already taken in at least one of: Homebrew, apt/other
package managers, PyPI, npm, or an existing unrelated CLI on a
developer's `$PATH`. Before committing to any specific name, do an
actual namespace check across whatever distribution channels this
project ships through. Precedent from adjacent tools (`aws`, `az`,
`gcloud`) favors a short but distinctive name over a generic noun —
worth considering something in that spirit (e.g. a 2-3 letter
project-specific abbreviation) rather than assuming "cloud" is safe to
take.

The rest of this doc uses `<bin>` as a placeholder for whatever name is
actually chosen.

## Design

### Binary/module rename

Go module path, `cmd/cloudcompose/` → `cmd/<bin>/`, build output,
`main.go`'s root `cobra.Command{Use: "<bin>"}`, and every
README/examples-README/CI-script reference change together — not a
"keep the old internal name, just alias the installed command"
approach, since a rename with the old name still visible in package
imports/error messages/`--help` output reintroduces the exact
two-names-for-one-thing confusion a rename is meant to remove.

**Migration path, not a hard break**: unlike the earlier draft of this
proposal, ship a transitional `cloudcompose` shim binary/script
alongside `<bin>` for at least one release that prints a deprecation
notice and execs through to `<bin>` with the same arguments, rather
than deleting `cloudcompose` outright the same day `<bin>` ships. Every
existing CI pipeline, shell alias, and script that currently invokes
`cloudcompose` should keep working, with a warning, until that period
ends.

### CLI restructure: `env` and `compose` subcommand groups

Cobra command tree changes from today's flat list
(`initCmd`/`mainCmd`/`upCmd`/`downCmd`/`psCmd`/`logsCmd`) to two grouped
parents, `env` and `compose` — a new pattern for this codebase, since no
nested cobra command exists today:

```
<bin> env up        # init (if needed) + terraform apply, env only
<bin> env init       # writes env-<name>/main.tf.json only, no apply
<bin> env down       # terraform destroy, env only (see multi-user-state.md's dependent-app check)
<bin> compose up     # compile + terraform apply, app only (today's `up`, minus its env step)
<bin> compose down   # today's `down`, unchanged behavior
<bin> compose ps     # today's `ps`, unchanged
<bin> compose logs   # today's `logs`, unchanged
<bin> compile        # unchanged: the explain/no-apply pipeline stays as-is, top-level (not nested)
```

Today's bundled `up`/app-only `down` are replaced by `compose up`/
`compose down`; `compose up` requires `--env` to point at an
already-`env up`'d directory (the applied-directory meaning `--env`
already has on `compile`/`ps`/`logs`/`down`), closing the flag-meaning
asymmetry `up.go`'s own doc comment currently calls out (`--env`
meaning the authored file on today's `up`, vs. the applied directory
everywhere else).

`<bin> compile` and `<bin> env init` both stay top-level, not nested
under `env`/`compose`: every other command runs real Terraform
(`apply`/`destroy`); these two only ever write `main.tf.json` and stop
— the same "no side effects, explain-friendly" pipeline stage
`compile --explain`/`--demo` already depend on not requiring any
applied environment or cloud credentials at all. Nesting them under
`env`/`compose` would incorrectly imply they belong to the
apply-oriented half of this tree.

`-f/--file` stays a persistent root flag; grouping `up`/`down`/`ps`/
`logs` under `env`/`compose` doesn't change that, since cobra resolves
persistent flags from any ancestor, and `-f` is only ever consumed by
`compose`/`compile`'s subcommands, never `env`'s.

### Naming collision to double check

`env` as a subcommand name risks a mild association with `.env`/shell
environment variables — a term Compose users already use for something
else. Probably fine given the surrounding context (`<bin> env up` reads
clearly), but worth watching for confusion in early feedback before
treating it as final.

## Non-goals

- Doesn't touch anything in `docs/multi-user-state.md` — that proposal
  uses today's `cloudcompose init`/`up`/`down` names throughout and
  should be read as applying equally to whatever this proposal's
  equivalents end up being, if both land.
- No decision here on distribution mechanics (Homebrew tap rename,
  Docker image tag, etc.) beyond noting they'd all need to follow the
  binary rename if approved.

## Implementation reference (once approved)

- Namespace-check the chosen `<bin>` name before any code changes.
- Repo-wide rename: Go module path, `cmd/cloudcompose/` → `cmd/<bin>/`,
  build output (`go build -o <bin> ./cmd/<bin>`), `main.go`'s root
  `cobra.Command{Use: "<bin>"}`.
- Transitional `cloudcompose` shim (script or thin Go wrapper) that
  execs to `<bin>` with a deprecation warning, removed after one
  release's migration window.
- `cmd/<bin>/` restructure into `env.go` (parent `env` + `up`/`init`/
  `down` subcommands) and `compose.go` (parent `compose` + `up`/`down`/
  `ps`/`logs` subcommands, absorbing today's `up.go`/`down.go`/`ps.go`/
  `logs.go`); `compile.go` stays a top-level command, unchanged in
  shape; remove today's bare `up`/`down` from root command registration
  once the shim's migration window closes.
- `README.md`, `examples/README.md`, CI scripts — update every
  `cloudcompose <verb>` reference to `<bin> <group> <verb>` as
  applicable.
