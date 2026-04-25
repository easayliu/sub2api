-- 103_rename_account_extra_max_sessions_to_max_devices.sql
-- Renames per-account extra JSON keys from session-oriented to device-oriented
-- to match the actual semantics of the limit (counts active devices, not
-- arbitrary "sessions"; see GenerateSessionHash priority order — Claude CLI
-- always falls on metadata.user_id.device_id, so multiple terminals/sub-agents
-- on the same machine count as ONE).
--
-- Two keys are renamed in the accounts.extra JSONB column:
--   max_sessions                  -> max_devices
--   session_idle_timeout_minutes  -> device_idle_timeout_minutes
--
-- Idempotent: only rows that still hold the old key are touched. If the new
-- key already exists on a row (re-running the migration after partial
-- success), it is preserved and the old key is dropped.
--
-- Safety:
--   * No data loss — values copied verbatim, then old keys removed.
--   * No schema change — accounts.extra stays JSONB.
--   * Compatible with concurrent reads: each UPDATE rewrites the entire
--     JSONB column atomically.

UPDATE accounts
SET extra = (extra - 'max_sessions') || jsonb_build_object('max_devices', extra->'max_sessions')
WHERE extra ? 'max_sessions'
  AND NOT (extra ? 'max_devices');

UPDATE accounts
SET extra = extra - 'max_sessions'
WHERE extra ? 'max_sessions';

UPDATE accounts
SET extra = (extra - 'session_idle_timeout_minutes') || jsonb_build_object('device_idle_timeout_minutes', extra->'session_idle_timeout_minutes')
WHERE extra ? 'session_idle_timeout_minutes'
  AND NOT (extra ? 'device_idle_timeout_minutes');

UPDATE accounts
SET extra = extra - 'session_idle_timeout_minutes'
WHERE extra ? 'session_idle_timeout_minutes';
