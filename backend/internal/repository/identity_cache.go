package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	fingerprintKeyPrefix   = "fingerprint:"
	fingerprintTTL         = 7 * 24 * time.Hour // 7天，配合每24小时懒续期可保持活跃账号永不过期
	maskedSessionKeyPrefix = "masked_session:"
	maskedSessionTTL       = 15 * time.Minute
)

// fingerprintKey generates the Redis key for account fingerprint cache,
// scoped by platform so each platform bucket holds an independent device_id.
func fingerprintKey(accountID int64, platform string) string {
	return fmt.Sprintf("%s%d:%s", fingerprintKeyPrefix, accountID, platform)
}

// legacyFingerprintKey returns the un-platformed key used before platform
// bucketing was introduced. Read-only; consulted as a one-shot fallback for
// the MacOS bucket so existing accounts keep their device_id without a flag day.
func legacyFingerprintKey(accountID int64) string {
	return fmt.Sprintf("%s%d", fingerprintKeyPrefix, accountID)
}

// maskedSessionKey generates the Redis key for masked session ID cache,
// also scoped by platform so the masked session_id stays stable per platform.
func maskedSessionKey(accountID int64, platform string) string {
	return fmt.Sprintf("%s%d:%s", maskedSessionKeyPrefix, accountID, platform)
}

// legacyMaskedSessionKey returns the un-platformed masked-session key. Used as
// a one-shot read fallback for the MacOS bucket only.
func legacyMaskedSessionKey(accountID int64) string {
	return fmt.Sprintf("%s%d", maskedSessionKeyPrefix, accountID)
}

// platformMacOS is the canonical MacOS bucket name. Centralized here so the
// cache layer can decide when to honor legacy un-platformed keys.
const platformMacOS = "MacOS"

type identityCache struct {
	rdb *redis.Client
}

func NewIdentityCache(rdb *redis.Client) service.IdentityCache {
	return &identityCache{rdb: rdb}
}

func (c *identityCache) GetFingerprint(ctx context.Context, accountID int64, platform string) (*service.Fingerprint, error) {
	key := fingerprintKey(accountID, platform)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		// One-shot legacy migration: pre-bucketing fingerprints lived under
		// `fingerprint:{accountID}` with the locked Mac profile, so adopt them
		// into the MacOS bucket on first access AND write-through to the new
		// key so subsequent reads hit the fast path. Without write-through the
		// service layer's needWrite gate stays false (locked-Mac fp matches the
		// MacOS profile, so applyLockedProfile reports no drift) and the legacy
		// key would expire silently within its 7d TTL window, dropping the
		// account's device_id.
		if err == redis.Nil && platform == platformMacOS {
			legacy, legacyErr := c.rdb.Get(ctx, legacyFingerprintKey(accountID)).Result()
			if legacyErr == nil {
				var fp service.Fingerprint
				if uErr := json.Unmarshal([]byte(legacy), &fp); uErr == nil {
					// Best-effort copy to new key (preserve original bytes to
					// keep ClientID and UpdatedAt verbatim). Failures here are
					// logged via Redis-side observability; the read path still
					// returns the parsed fp so the request succeeds.
					_ = c.rdb.Set(ctx, key, legacy, fingerprintTTL).Err()
					return &fp, nil
				}
			}
		}
		return nil, err
	}
	var fp service.Fingerprint
	if err := json.Unmarshal([]byte(val), &fp); err != nil {
		return nil, err
	}
	return &fp, nil
}

func (c *identityCache) SetFingerprint(ctx context.Context, accountID int64, platform string, fp *service.Fingerprint) error {
	key := fingerprintKey(accountID, platform)
	val, err := json.Marshal(fp)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, val, fingerprintTTL).Err()
}

func (c *identityCache) GetMaskedSessionID(ctx context.Context, accountID int64, platform string) (string, error) {
	key := maskedSessionKey(accountID, platform)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			// Legacy migration for MacOS bucket only (mirrors fingerprint path).
			// Write-through to the new key so the bucket is bootstrapped even
			// if the caller skips its own Set (e.g., masking disabled mid-flight).
			if platform == platformMacOS {
				if legacy, lErr := c.rdb.Get(ctx, legacyMaskedSessionKey(accountID)).Result(); lErr == nil {
					_ = c.rdb.Set(ctx, key, legacy, maskedSessionTTL).Err()
					return legacy, nil
				}
			}
			return "", nil
		}
		return "", err
	}
	return val, nil
}

func (c *identityCache) SetMaskedSessionID(ctx context.Context, accountID int64, platform, sessionID string) error {
	key := maskedSessionKey(accountID, platform)
	return c.rdb.Set(ctx, key, sessionID, maskedSessionTTL).Err()
}
