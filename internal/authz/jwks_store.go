package authz

import (
	"context"

	authkit "github.com/disillusioned-labs/platform/authkit"
	goredis "github.com/redis/go-redis/v9"
)

// jwksStoreKey holds the raw JWKS document. One value per Redis database, not
// per instance: every replica reads the same document identity published.
const jwksStoreKey = "authkit:jwks"

// RedisJWKSStore persists the raw JWKS document so a restarting expense can
// verify tokens while identity is down, instead of rejecting every request
// until identity comes back.
//
// The document carries no TTL on purpose: its whole value is surviving an
// outage longer than any expiry we could pick. Staleness is bounded by the
// next successful fetch, which overwrites the value; an unknown kid after a
// rotation falls through to that fetch as usual.
type RedisJWKSStore struct {
	client *goredis.Client
}

var _ authkit.Store = (*RedisJWKSStore)(nil)

// NewRedisJWKSStore builds the store on top of an existing Redis client.
func NewRedisJWKSStore(client *goredis.Client) *RedisJWKSStore {
	return &RedisJWKSStore{client: client}
}

// Load returns the persisted JWKS document, or ErrNoStoredJWKS when nothing
// has been saved yet (fresh Redis, or Redis was never configured).
func (s *RedisJWKSStore) Load(ctx context.Context) ([]byte, error) {
	raw, err := s.client.Get(ctx, jwksStoreKey).Bytes()
	if err != nil {
		if err == goredis.Nil {
			return nil, authkit.ErrNoStoredJWKS
		}
		return nil, err
	}

	return raw, nil
}

// Save overwrites the persisted JWKS document. Callers treat a failure as a
// degraded fallback, never as a request failure.
func (s *RedisJWKSStore) Save(ctx context.Context, jwks []byte) error {
	return s.client.Set(ctx, jwksStoreKey, jwks, 0).Err()
}
