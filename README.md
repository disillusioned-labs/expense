# expense

Service expense management: transaksi, dokumen nota (OCR), approval multi-step, laporan.

**Status: boilerplate — bisa boot, belum ada fitur.** Repo diinisialisasi mengikuti struktur service lain (lihat `identity/`). Yang sudah ada: skema lengkap sebagai migrasi goose, konfigurasi sqlc, script Makefile, lifecycle app (`cmd/api` + `internal/app` + `internal/config` + `internal/server`) yang **sudah teruji boot**: `/healthz`, `/readyz` (cek postgres), envelope error 404/405, rate limit `/api/v1`, drain graceful — tanpa satu pun endpoint bisnis. Yang **belum**: service & handler per resource (project, transaction, document, approval — dikosongkan sengaja), `db/queries` + repository hasil sqlc, worker outbox. Urutan implementasinya: [docs/how-to/todo-detail.md](../docs/how-to/todo-detail.md) bagian "Fondasi (M0)" dan "expense".

## Isi saat ini

```
cmd/api/main.go                 entry point: load config → app.RunAPI
internal/
├── app/
│   ├── api.go                  lifecycle: telemetry → postgres → migrasi (embed) → redis → serve → drain
│   ├── di.go                   wiring - baru infrastruktur; service per resource ditambah di sini
│   └── version.go              build provenance (ldflags)
├── config/config.go            env + .env + defaults tervalidasi (port 8081, tanpa Auth)
├── server/
│   ├── server.go               chi: healthz/readyz, envelope 404/405, rate limit /api/v1,
│   │                           /api/v1 kosong menunggu route resource
│   ├── middleware.go           request logger, recover, route pattern → otel
│   └── pprof.go                listener loopback terpisah
├── handler/
│   ├── respond.go              envelope {"data"}/{error}, WriteJSON, WriteServiceError
│   ├── validate.go             DecodeValid (1 MiB, DisallowUnknownFields, 422 fields)
│   ├── page.go                 limit/offset (default 20, maks 100)
│   └── health/handler.go       /healthz + /readyz (drain-aware)
├── service/errors.go           domain error expense (APPROVER_*, TRANSACTION_LOCKED, dst.)
└── repository/                 BELUM ADA - dibuat `make sqlc` saat query pertama ditulis
db/
├── migrations/               14 migrasi goose, semua tabel rancangan:
│                             projects, project_members, transactions, documents,
│                             approval_rules, approval_batches, approvals,
│                             approval_decisions, outbox_events,
│                             organization_members + organizations (PROYEKSI read-only, D1),
│                             view approval_batch_status, dan REVOKE append-only
├── migrations.go             embed.FS - migrasi diterapkan dari binary saat boot
└── queries/                  kosong - query SQL ditulis per fitur saat implementasi,
                             lalu `make sqlc` men-generate internal/repository/.
                             Catatan: `make sqlc` sengaja gagal ("no queries") sampai
                             query pertama ada - itu perilaku sqlc yang benar, bukan rusak.
sqlc.yaml                    engine postgresql, pgx/v5, emit_interface - persis identity
Makefile                     run, build, test, lint, vuln, sqlc, sqlc-diff, migrate-*, docker-*
.env.example                 set penuh yang dibaca internal/config (persis kosakata identity)
.env.compose                 override address lewat jaringan compose (data/messaging/observability)
Dockerfile                   multi-stage: build → distroless runtime, EXPOSE 8081
docker-compose.yml           service api (worker menyusul di M4), jaringan infra eksternal
.github/workflows/ci.yml     lint, test, integration, vuln, docker + smoke test
                             (job sqlc-diff & config test diaktifkan bersama query/test pertama)
.golangci.yml, .dockerignore, .gitattributes - persis identity
```

Keputusan desain di balik skema: [docs/reference/skema-database.md](../docs/reference/skema-database.md) bagian "Database `expense` (Rancangan)" dan [docs/explanation/expense/README.md](../docs/explanation/expense/README.md) section 8 (D1–D10).

## Menjalankan

```bash
cp .env.example .env                       # DSN mengarah ke DB expense (scripts/init-db.sql)
make run                                   # boot: migrasi (embed) → :8081
make build                                 # binary ke ./bin

curl localhost:8081/healthz                # {"status":"ok"}
curl localhost:8081/readyz                 # {"checks":{"postgres":"up"}}

make migrate-up                            # goose up semua 14 migrasi
make migrate-down                          # rollback satu migrasi
make migrate-status
```

Uji naik-turun sebelum ada data: `make migrate-up` lalu `make migrate-down` sampai nol lalu naik lagi — membuktikan `Down` benar. Lalu buktikan batas append-only nyata:

```bash
psql "postgres://expense_app:devpassword@localhost:5432/expense" \
  -c "UPDATE approval_decisions SET notes='x'"     # HARUS ditolak Postgres
```

## Aturan kerja

Mengikuti [../identity/AGENTS.md](../identity/AGENTS.md): migrasi → query → `make sqlc` → service → handler, wiring hanya di `di.go`/`server.go`; satu service tidak memanggil service lain; tipe service tanpa struct tag. Penambahan tabel baru = migrasi baru, bukan mengubah migrasi lama.
