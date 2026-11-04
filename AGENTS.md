# Agent Notes

- Keep these instructions in English. This repository owns the Remote AI Go backend, its SQL upgrades,
  tests, build scripts and CI. The retired NestJS implementation must not be restored.
- The Go module is `github.com/piperhex/remoteai-admin`; the executable entry point is `cmd/admin`.
- Keep production Compose service `admin-go`, `ADMIN_GO_*` variables, network aliases and image names
  compatible with existing deployments. A source directory rename does not authorize deployment.
- Follow `DEPLOYMENT.md`. Use `scripts/deployment_compose.py check`, `up`, and `verify` for updates and
  rollbacks. Preserve all Compose files recorded by the running container, including private overrides
  and `compose.desktop-relay.yml`. Verify relay ports and networking before and after deployment.
- Store database upgrades in `sql/`. Check Kong route and authentication coverage with
  `go test ./internal/server -run TestKong` when adding or changing endpoints.
- Run `gofmt`, `go vet ./...`, `go test ./...`, `node scripts/check-contract.mjs`,
  `npm run check:i18n`, and deployment guard tests for relevant changes. Do not weaken tests.
- `legacy-contract.json` and `validation-oracle.json` are frozen regression fixtures with provenance in
  `testdata/legacy-source.json`. New behavior belongs in Go code and independent Go tests.
- Translatable backend platform messages are owned by `piperhex/remoteai-i18n` and consumed through its
  immutable npm-compatible GitHub release. Run `npm ci`, `npm run generate:i18n` and `npm run check:i18n`.
  Edit sources in that repository, publish a new version, then update this dependency and lockfile.
  Do not edit generated Go copies or maintain a separate translation package here.
- Admin UI is a separate repository supplied to Docker as the named `admin-ui` build context.
  Do not import parent or sibling files in ordinary builds. Explicit cross-client integration tests
  may use a parent checkout identified by `REMOTEAI_SOURCE`.
- Keep functions small, responsibilities focused, errors explicit, and credentials out of source and logs.
- Commit and push verified changes here before the parent updates its submodule gitlink.
