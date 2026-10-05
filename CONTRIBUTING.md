# Contributing to Ariad

## Commit messages

Ariad commits use this project-specific Conventional Commit form:

```text
type(scope): concise imperative summary
```

The scope is required and identifies the boundary changed by the commit. Keep
the complete header at or below 100 characters.

Examples:

```text
feat(retrieval): add reciprocal rank fusion
fix(widget): preserve the submitted idempotency key
test(security): reject cross-workspace agent access
ci(repo): validate commit messages
docs(docs): record the model provider decision
chore(deps): update the frontend toolchain
```

Allowed types:

- `feat`: externally observable product capability
- `fix`: defect correction
- `perf`: measured performance improvement
- `refactor`: behavior-preserving implementation change
- `test`: test or fixture changes
- `docs`: documentation only
- `build`: build system or generated-code pipeline
- `ci`: continuous-integration configuration
- `chore`: repository maintenance with no product behavior change
- `revert`: an explicit revert

Allowed scopes follow Ariad's repository and domain boundaries:

```text
agent api auth console contracts conversation db deploy deps docs evaluation
ingestion knowledge platform repo retrieval runtime security tenant widget worker
```

Breaking changes use `!` and a `BREAKING CHANGE:` footer:

```text
feat(api)!: replace the public session token contract

BREAKING CHANGE: clients must exchange the public key for a scoped session token.
```

The subject should be concise, imperative, and lower-case where the language has
case. Do not end it with a period. Use the body for motivation and tradeoffs,
and wrap body and footer lines at 100 characters.

Validate a message before committing:

```bash
printf '%s\n' 'feat(agent): add immutable version model' | npm run check:commit
```

CI validates every commit in a pull request and every commit pushed directly to
`main`. Merge and automatic revert messages recognized by commitlint are ignored.

## Repository checks

Before opening a pull request, run:

```bash
npm run check
```

For Go-only iteration, run:

```bash
make check-go
```

The Makefile's explicit `GO_PACKAGES` value is the canonical Go package set.
Do not replace it with root wildcard commands such as `go test ./...`,
`go vet ./...`, or `go list ./...`: installed npm dependencies can contain Go
source files under `node_modules` and are not part of Ariad's Go module surface.
