package app

import (
	"log/slog"

	"github.com/disillusioned-labs/expense/internal/server"
	"github.com/disillusioned-labs/platform/cache"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// buildDeps wires the dependencies the router needs. Wiring churn stays in
// this file; app/api.go never changes.
func buildDeps(
	pool *pgxpool.Pool,
	rdb *goredis.Client,
	redisRequired bool,
	svcCache cache.Cache,
	_ *slog.Logger,
) (server.Deps, error) {
	return server.Deps{
		Pool:          pool,
		Redis:         rdb,
		RedisRequired: redisRequired,
		Cache:         svcCache,
	}, nil
}
