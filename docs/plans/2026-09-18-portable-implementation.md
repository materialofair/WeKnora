# Portable Knowledge Assistant Implementation Plan

**Goal:** Deliver the approved Go-backed Electron/Web knowledge assistant while preserving the capability baseline and removing the seven excluded data-source connectors and mini-program client.

**Architecture:** Reuse the existing Go service and Vue application in this fork. Add a portable runtime profile independent of the edition, so embedded SQLite/local storage do not disable organizations, authentication, Agent, Wiki, memory or integrations. Distribute the same service with Web assets and an Electron supervisor; parser and sandbox backends retain explicit capability reporting.

**Tech Stack:** Existing Go/Vue/TypeScript, SQLite FTS5/sqlite-vec, Electron, native document conversion components.

## Authorization and execution

The user completed the requirements interview and explicitly approved development. No additional design permission is needed. Work in the existing fork, preserve its license/history, and reuse application modules rather than create a second divergent implementation. The resulting distribution has its own launcher and packaging. Model/Feishu credentials are supplied at runtime and are never committed.

## 1. Portable service runtime

- [x] Add a testable startup package and `cmd/server` flags for portable profile, data directory, assets directory, host and port.
- [x] Generate and persist JWT and credential-encryption keys in the writable data directory; never ship fixed credentials or write into installation resources.
- [x] Use SQLite, sqlite retrieval, local storage and memory streaming without Redis; retain standard edition authentication and organization capabilities.
- [x] Resolve config/migration/web resources independently of shell working directory. Serve Web assets without treating MCP/resource/API routes as SPA routes.
- [x] Fail startup on migration failure in portable mode. Avoid destructive dirty-migration recovery.
- [x] Validate first launch, repeated launch, invalid data/config and static/API route behavior. Startup-profile repeatability, corrupt secrets, missing config and static/API behavior have unit coverage; full packaged launch is recorded in integration evidence.

Files: `internal/portable/`, `cmd/server/`, `internal/config/config.go`, `internal/router/static.go`, `internal/router/router.go`, focused migration initialization in `internal/container/container.go`.

## 2. Durable local background tasks

- [x] Add a database-backed task executor compatible with existing task handlers, using bounded workers and durable states instead of unlimited goroutines.
- [x] Persist delayed work, retries and task IDs; record execution outcomes and recover interrupted work with explicit at-least-once semantics.
- [x] Start workers only after handlers register; integrate clean shutdown and avoid resetting recoverable document tasks to failed.
- [x] Verify delayed/retry execution, restart recovery, duplicate IDs and cancellation where supported; document limits for external side effects.

Files: `internal/router/sync_task.go`, new local task executor/tests, task container wiring and startup recovery.

### Runtime/task validation recorded on macOS arm64

- `go test -tags sqlite_fts5 ./internal/portable ./internal/router ./internal/database ./cmd/server` passed. Coverage includes stable keys, refusal to generate replacement keys for an existing database, exclusive data-directory locking, SPA/backend namespace separation, migration resource paths and preserving a dirty migration state.
- `go test -race -tags sqlite_fts5 ./internal/router -run TestLocal` passed. SQLite queue tests exercise retrying transient outcome-write failures without re-running handlers, delay/retry, duplicate IDs, reusable completed IDs, bounded four-worker concurrency, shutdown/reopen recovery, cancellation and runtime inspection/actions.
- Portable task ID and retry metadata now flow through the same service helpers as Redis/Asynq metadata; focused `TestPortableTaskMetadata` and existing task tests passed.
- Local tasks deliver at least once. Handlers must tolerate replay of external effects and cooperate with context cancellation; there is no unsafe process-kill substitute for cancellation. Archived failures remain available for explicit rerun/purge. Completed records obey retention, with zero-retention IDs released immediately for Wiki debounce compatibility.

### Durable evaluations and offline backup

- [x] Portable evaluation records persist complete details and progress, isolate reads/updates by tenant, return snapshots and propagate persistence errors. Restart marks pending/running evaluations failed instead of replaying model calls or temporary-knowledge side effects.
- [x] Add `assistant-backup --data-dir PATH --archive PATH backup|restore`. Backup requires the same exclusive data lock as the server and includes encryption keys, DB/WAL and files. Restore creates a new directory, rejects traversal/links/special entries, verifies gzip checksum, and removes its own incomplete destination on failure.
- `go test -race -tags sqlite_fts5 ./internal/application/service -run 'TestEvaluation|TestMemoryEvaluation'` passed, including SQLite restart, tenant isolation, snapshot ownership, write failures and concurrent progress updates.
- `go test ./internal/portable ./cmd/assistant-backup` passed, including backup/restore, active-server refusal, unsafe archive paths/links and truncated archive cleanup. Portable package cross-compilation passed for `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c` and Linux amd64. Windows runtime behavior has not been exercised here.

## 3. Electron and Web

- [x] Add an Electron application that launches the packaged Go service on loopback with a writable user-data directory, waits for health, and shows the existing Web UI.
- [x] Support a configured remote server URL using the same frontend and normal backend authentication.
- [x] Keep renderer sandbox/context isolation, disable Node integration, restrict navigation/popups and IPC origins. Do not expose local process/secret management to a remote renderer.
- [x] Handle single-instance use, startup errors, logs, shutdown and backend process cleanup. Disable public auto-update behavior in the new distribution.
- [x] Add lifecycle/unit tests and packaging for Windows/macOS; provide a local runnable artifact where the host permits.

Files: `desktop/`, minimal frontend integration if required; no wholesale UI rewrite.

## 4. Document parsing and distribution

- [x] Include working PDF/Office conversion in the no-Docker distribution; verify actual samples rather than advertise an unavailable parser.
- [x] Reuse anydoc where platform-supported; explicitly handle PDF/scan capabilities and legacy Office limitations rather than treating all formats as simple text.
- [x] Add reproducible per-platform build/package scripts with frontend assets, config, migrations, licenses and launch instructions. Fail packaging when required components are missing.
- [x] Keep runtime installation independent of npm/Go/Rust network access. Build tools and artifacts are prepared before transfer to the company network.

Files: parser/build integration, `scripts/` packaging, release workflow and user deployment documentation.

## 5. Scope trimming and complete feature integration

- [x] Remove GitLab/Notion/Confluence/Yuque/DingTalk/RSS/IMA data-source registration and implementation plus their exclusive UI/config/tests; retain Feishu, local files, URL and manual ingestion.
- [x] Remove the mini-program client and its dedicated tests/docs. Do not conflate data-source connectors with IM connectors.
- [x] Provide embedded graph storage for portable GraphRAG through the existing graph interface, with namespace filtering, update/delete behavior and tests.
- [x] Preserve remote sandbox providers and Skills/MCP/long-term-memory workflows. Explicitly report unavailable local strong isolation rather than adding an unsafe shell fallback.
- [x] Document local sandbox and cross-platform validation boundaries. No model or private Feishu service is fabricated for verification.

## Verification and review

- [x] Run focused Go tests with `-tags sqlite_fts5`, frontend build and existing relevant frontend tests, Electron lifecycle tests and syntax checks.
- [x] Build the real service, start it in a temporary data directory without Docker/Redis/PostgreSQL, verify health, Web UI, first-user auth, persistence and available capabilities.
- [x] Import representative text/PDF/Office files and validate parsed content; use deterministic model fixtures for local pipeline tests where appropriate, clearly distinguish them from real internal-model validation.
- [x] Review code/security and fix actionable findings. Record actual tested OS and produced artifacts, and do not claim Windows/macOS/remote-service behavior that was not exercised.

## Pending environment-dependent acceptance

Company credentials/network, target server OS and local strong-isolation privileges are not available. Preserve configurable interfaces and report these as explicit unverified requirements. The approved overall capability scope remains unchanged; a built shell alone is not a completed product.

## 实际交付验证

见 [验证记录](../portable-validation.md) 与 [部署说明](../portable.md)。以上勾选表示该开发/验证动作已执行，跨平台、OCR及企业服务未验收部分以验证记录为准，不能解释为所有环境已通过。

## SQLite Wiki compatibility verification

- `go test -tags sqlite_fts5 ./internal/application/repository -run 'TestWiki|TestSessionRepository'` passed after adding SQLite branches for Wiki source-reference lookups, batch summary lookups, filtered listing, regex search and similar-title lookup.
- Source references are decoded with `json_each` and matched case-sensitively as an exact ID or `ID|` prefix; tests cover quoted/backslash/wildcard IDs, titles, KB separation, archived summaries and deleted pages.
- SQLite Wiki `Search` uses case-insensitive Go RE2 expressions, streaming rows and retaining bounded top-k results with the same title/slug/summary/content ranking. PostgreSQL retains its existing regex SQL. PostgreSQL-only regex constructs such as backreferences are rejected by RE2. SQLite search and trigram candidate selection scan the selected KB rather than relying on PostgreSQL indexes.
- SQLite session-title filtering now specifies `ESCAPE '\'`; `%`, `_` and backslash are verified as literal search characters.
