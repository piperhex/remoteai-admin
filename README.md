# Remote AI Admin

Independent Go backend for Remote AI, using Gin, PostgreSQL/GORM and Redis.
The management interface lives in [remoteai-admin-ui](https://github.com/piperhex/remoteai-admin-ui).
This repository preserves the history of `apps/admin-go`; the parent now mounts it at `apps/admin`.

## Development

Install the Go toolchain declared in `go.mod`, Node.js 22 for fixture and translation checks, PostgreSQL
and Redis. Copy `.env.example` to `.env`, configure local credentials, then run:

```sh
go run ./cmd/admin
go build -o dist/ ./cmd/admin
npm run check
```

For the interface, clone `remoteai-admin-ui` alongside this repository, run `npm ci`, then `npm run dev`.
Open `http://127.0.0.1:1421/admin.html`; its development proxy connects to backend port 8080.
The UI build writes to its own `dist/`. Set `PUBLIC_DIR` to those built assets when serving them locally.

## Images and deployment

From this repository, with the UI checkout at `../admin-ui`:

```sh
docker build --target verify -t remoteai-admin:verify .
docker build --build-context admin-ui=../admin-ui -t remoteai-admin:local .
```

Compose also accepts `ADMIN_UI_CONTEXT` to select another UI checkout. The runtime image packages the
Go executable, built UI and CA certificates. No parent checkout is required to build or run the backend.

Follow [DEPLOYMENT.md](DEPLOYMENT.md) and use the `check`, `up` and `verify` commands of
`scripts/deployment_compose.py`. Preserve existing Compose service `admin-go`, `ADMIN_GO_*` variables,
network aliases, volumes, image names and all recorded deployment Compose files. The repository rename
is not a production migration. Existing server deployment directories should remain in place.

- `compose.yml`: Go service on existing database and Kong networks.
- `compose.infrastructure.yml`: optional standalone PostgreSQL and Redis setup.
- `compose.desktop-relay.yml`: TURN relay; retain it for every update and rollback when enabled.
- `compose.chat-connectivity.yml`: optional discovery service. Building it requires `REMOTEAI_SOURCE`
  pointing to a parent checkout that owns `crates/chat-connectivity/Dockerfile`.
- `kong/existing-kong.example.yml`: production route and authentication baseline.
- `sql/`: explicit upgrades for existing installations; empty databases bootstrap from embedded migrations.

## Verification

```sh
npm run check
go test ./internal/server -run TestKong -count=1
python3 -B -m unittest discover -s scripts -p 'test_deployment_*.py' -v
```

Set `COMPOSE_CONFIG_TEST=1` to include the real Docker Compose merge check. The `verify` Docker target
runs race tests with an isolated Redis server. Windows uses `py -3` if Python is not on PATH.

`internal/platform/legacy-contract.json` and `testdata/validation-oracle.json` preserve the old route,
DTO and validation behavior. `node scripts/check-contract.mjs` verifies their recorded hashes and source
revision. Go tests verify current routing, authorization and compatibility without NestJS source.

Historical live comparisons remain explicitly optional: import an archived local fixture image and set
`LEGACY_ADMIN_IMAGE` before `node scripts/setup-parity.mjs`. The image is used only by the `legacy`
profile in `compose.test.yml`, never by production Compose. There is no NestJS build target or source.
Install fixture dependencies with `npm ci --prefix testdata/tools`, start the Go profile, then run
`node scripts/parity.mjs`. These historical comparisons do not replace current Go feature tests.

Cross-client `bulk-relay-smoke.mjs`, `port-stream-smoke.mjs` and opt-in browser/native TURN tests require
`REMOTEAI_SOURCE` pointing to an initialized parent checkout with its client test dependencies. Windows
browser tests use bundled Playwright Chromium with the parent's password-probe protection flags.

For fast local Docker iteration, `scripts/build-local.ps1` builds a Linux executable into `testdata/bin/`;
`compose.local.yml` and `testdata/Dockerfile.local` use that executable instead of recompiling inside Docker.

## Reference

See [REFERENCE.md](REFERENCE.md), [CLIENT_READ_CACHE.md](CLIENT_READ_CACHE.md),
[DESKTOP-RELAY.md](DESKTOP-RELAY.md), [TELEMETRY.md](TELEMETRY.md), and the focused notes in `docs/`.
Backend platform copy is maintained only in `packages/i18n/platform/`. After edits run
`npm run generate:i18n` and `npm run check:i18n`.
