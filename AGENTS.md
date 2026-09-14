# glab CLI agent instructions

Go-based GitLab CLI. Entrypoint: `cmd/glab/main.go`. Commands live under
`internal/commands/<noun>/<verb>/` (noun-first grammar, for example,
`glab mr create`).

## Common tasks

For each common task, the one non-discoverable pointer the agent can't
infer from reading the tree.

- **Adding or editing a command** — first read the matching block of
  [`.gitlab/duo/mr-review-instructions.yaml`](.gitlab/duo/mr-review-instructions.yaml).
  It is the canonical source for code conventions (command structure,
  flag handling, IO streams, API client and pagination, test setup,
  test discipline) and is what GitLab Duo Code Review grades MRs
  against. Blocks are scoped by `fileFilters`, so only the ones that
  match the file you are editing apply.
- **Writing a comment** — default to not writing one. Document only what a
  competent Go reader cannot infer from the code; a comment that restates
  the line below it is noise, and rewording it does not help. Two gates run
  in lefthook pre-commit and the `lint:comments` CI job:
  `go run ./scripts/comment-overlap` rejects comments whose words are already
  carried by the adjacent code, and `gocritic`'s `commentedOutCode` rejects
  commented-out code. `go run ./scripts/comment-ratio -base origin/main`
  reports comment volume on a change and is advisory. Godoc on exported
  symbols is exempt from the overlap gate by position, so it is not flagged
  for repeating its own symbol name. Full rules: the `Comments` block of
  [`.gitlab/duo/mr-review-instructions.yaml`](.gitlab/duo/mr-review-instructions.yaml).
- **Looking for a helper before writing a new one** — the most common
  feedback on this project is "use the existing helper":
  - `internal/cmdutils/` — flag wiring (`EnableRepoOverride`,
    `EnableJSONOutput`, `NewEnumValue`), the `Factory` interface.
  - `internal/testing/cmdtest/` — test setup (`SetupCmdForTest`,
    `NewTestFactory`, `WithStdin`/`WithBranch`/`WithGitLabClient`).
  - `internal/iostreams/` — all output (`LogInfo*`, `LogError*`,
    `PrintJSON`) and interactive prompts (`Confirm`, `Input`, `Select`).
  - `internal/glrepo/` — repository interface.
  - `internal/tableprinter/` — formatted text output.
  - `internal/text/` — `ExperimentalString`, `BetaString`.
  - `internal/config/schema.go` — every configuration key is registered
    in `KeySchema`. Add a `KeyDef` entry to introduce a new key (it
    drives the blank config, `config set` validation, defaults, env-var
    resolution, legacy aliases, and keyring eligibility).
- **Copying from a canonical example**:
  - New command: `internal/commands/gpg-key/get/get.go`.
  - Paginated list command: `internal/commands/securefile/list/list.go`.
  - Command tests with API mocks:
    `internal/commands/gpg-key/get/get_test.go`.
  - JSON output assertion test:
    `internal/commands/runnercontroller/token/list/list_test.go`.
- **Updating a command's docs** — author the change in the Go source
  (`Short`, `Long`, `Example`, flag descriptions on the `cobra.Command`),
  run `make gen-docs`, commit the regenerated files under `docs/source/`
  in the same commit. Never edit `docs/source/` directly. `make gen-docs`
  also prunes pages for removed, deprecated, and hidden commands (and
  deletes any emptied directories), so never hand-delete pages under
  `docs/source/`. Instead regenerate the docs and commit the deletions.
- **Reviewing MR feedback** — use the `glab` CLI (or `glab` MCP tools)
  end-to-end, not raw `glab api`:
  - Fetch: `glab mr view <id> --output json`.
  - Reply: `glab mr note create --reply <discussion-id> --message "..."`.
  - Resolve: `glab mr note resolve <discussion-id>`. Pass **only** the
    discussion ID — a trailing MR ID argument errors out.
  - Add a new inline diff comment:
    `glab mr note create --file <path> --line <N>` (or `--line N:M`, or
    `--old-line N` for a deletion).
- **Creating an MR that references an issue** — add
  `/copy_metadata #<issue-id>` on its own line in the description.
  GitLab copies the issue's labels and milestone, so the
  `glab mr create --label` flags aren't needed.
- **Naming a new command or writing a commit message** — verbs follow
  noun-first grammar (`create`, `list`, `get`, `update`, `delete`);
  see `CONTRIBUTING.md` "Grammar" before introducing a new verb.
  Commit messages are Conventional Commits, enforced by the
  `commit-msg` hook through `scripts/commit-lint` (needs Node.js).

## Verify changes before you push

Lefthook runs automatically on `git push`. Install it once with
`lefthook install`. Install all tools with `make bootstrap`, which uses
`mise` and `.tool-versions`.

```shell
lefthook run pre-push  # build, lint against origin/main, test-changed, generated-doc/code check, markdown/vale/lychee
make check             # test + lint
```

To skip hooks, use `LEFTHOOK=0 git push` or
`LEFTHOOK_EXCLUDE=pre-push git push`. Treat these like `--no-verify`, an
escape hatch for debugging the hooks themselves, not a workaround for slow
builds. The hooks catch generated-doc/code drift and lint regressions
before merge.

## Common commands

```shell
make build                                    # compile to ./bin/glab
make lint                                     # golangci-lint (full)
make fix                                      # golangci-lint --fix + gofmt + goimports
make test                                     # all unit tests (gotestsum, writes coverage.txt/xml)
make test-changed                             # tests changed packages + reverse deps against origin/main
make test-race                                # unit tests with -race
go test ./internal/commands/mr/note/...       # single package
go test ./internal/commands/mr/note/... -run TestCreate
make gen-docs                                 # regenerate docs/source/** from cobra definitions
make generate                                 # go generate ./... (includes config stubs)
make gen-config                               # config stubs from internal/config/config.yaml.lock
```

`make test` forcibly clears `VISUAL`, `EDITOR`, `PAGER`, and `GITLAB_TOKEN`,
and sets `CI_PROJECT_PATH` from the origin remote. Some tests depend on
this. If you run `go test` directly and see environment-dependent failures,
replicate that setup.

> [!note]
> Local vendor workflow: `vendor/` is gitignored. Do not request vendor
> updates in merge requests. On an inconsistent-vendoring error,
> run `go mod vendor` to resync. Do not use `-mod=mod`, which bypasses the
> vendor directory instead of fixing it.

## Integration tests

Integration tests are tagged `//go:build integration`, use the file suffix
`_integration_test.go`, and use the test name suffix `_Integration`. They
are not run by `make test`. To run them, use `make integration-test-race`,
which adds `-tags=integration`. They call a real GitLab instance. Locally
they are skipped unless both `GITLAB_TEST_HOST` and `GITLAB_TOKEN_TEST` are
set (see `GetHostOrSkip` in `test/helpers.go`). The token must have the
`api` scope. The
`glab duo` tests require a GitLab Duo-enabled user.

## Environment variables

- `GITLAB_TOKEN` — API token. Overrides configuration.
- `GITLAB_HOST`, `GITLAB_URI`, `GL_HOST` — Default GitLab instance,
  outside Git repositories.
- `GLAB_CONFIG_DIR` — Overrides the configuration directory. Highest priority.
- `GLAB_ENABLE_CI_AUTOLOGIN=true` — Together with `GITLAB_CI=true`,
  enables `CI_JOB_TOKEN` auto-login.
- `GLAB_DEBUG=true` — Verbose logging for Git commands, expanded aliases, and
  DNS.
