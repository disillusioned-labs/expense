# expense

Expense management API and workers for projects, transaction drafts, receipt OCR, and multi-step approvals.

## Current scope

The API serves project and role administration, project membership, transaction CRUD and submission, receipt/document access, approval decisions and rules, and aging views. Outbox, OCR consumer, reminder, gRPC, and worker entrypoints are present. Payment/payout, reporting, and CSV exports are not implemented.

The `expense` database owns expense records. It does **not** copy `organizations` or `organization_members`; authorization and active organization membership are checked through the identity service. Project membership is stored locally in `project_members`, with optimistic versioning for concurrent role changes. Cross-database membership revocation cannot share an atomic transaction, so protected requests must continue to enforce current access.

The request path is `internal/handler/<resource>` → `internal/service/<use-case>` → `internal/repository` (sqlc/pgx). Migrations live in `db/migrations/` and queries in `db/queries/`; generated repository code must be refreshed with `make sqlc`, never edited by hand.

## Run locally

```bash
cp .env.example .env
make run
```

The local `.env` selects the database and infrastructure endpoints and opts into development migration behavior. The API exposes `/healthz` and `/readyz`; business endpoints are under `/api/v1` and require the configured identity service.

## Checks

```bash
make test                 # unit tests
make test-integration     # PostgreSQL integration tests; requires Docker
make sqlc-diff            # check generated repository files are current
make lint
```

There are 16 Goose migrations at this revision. Integration tests use Testcontainers and the embedded migrations; migrations 00001–00016 include the project member version used by `expected_version` placement updates.
