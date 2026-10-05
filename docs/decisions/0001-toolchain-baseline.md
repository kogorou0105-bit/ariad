# ADR-0001: Toolchain baseline

- Status: Accepted
- Date: 2026-10-06

## Context

The initial scaffold used the newest locally available Go and current npm
package releases. Exact dependency versions improve reproducibility, but adopting
new major versions without an explicit support policy can expose the foundation
to ecosystem compatibility churn.

## Decision

Ariad supports one pinned Foundation baseline:

- Node.js 24.19.x and npm 12.x;
- Go 1.26.5;
- React 19.3.0;
- TypeScript 7.0.2;
- Vite 8.3.2;
- ESLint 10.12.0 with typescript-eslint 8.71.1;
- golangci-lint 2.14.0.

The baseline is intentional rather than a floating “latest” policy. Node and npm
major ranges are bounded in `package.json`; `.nvmrc`, `.go-version`, `go.mod`, and
CI select the tested runtime versions. JavaScript packages remain exact in the
lockfile and workspace manifests.

## Upgrade policy

- Dependency upgrades are reviewed changes and must pass the full repository gate.
- Runtime major upgrades require a new or superseding ADR.
- Patch and minor upgrades may be grouped, but generated lockfile changes are reviewed.
- Before adding a provider SDK or code generator, compatibility with this baseline is tested in CI.
- The baseline is reviewed at least once per quarter and before a public release.

## Consequences

Using recent major versions may still expose compatibility gaps in third-party
libraries. Pinning the baseline makes that risk visible and reproducible; it does
not eliminate it. If a required dependency is incompatible, prefer a documented
baseline downgrade over local workarounds or floating version ranges.
