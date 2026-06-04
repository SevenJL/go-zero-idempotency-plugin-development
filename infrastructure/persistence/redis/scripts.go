package redis

import (
	"github.com/redis/go-redis/v9"
)

// Lua scripts that implement the atomic idempotency operations described in
// the development documentation §8.3.
//
// All scripts use cjson.decode() for robust JSON field extraction instead of
// Lua string.find() pattern matching. cjson is available on Redis ≥ 2.6 when
// compiled with Lua support (the default for all major Redis distributions).
// This avoids edge cases where:
//   - Field values contain escaped quotes (\") that break regex patterns
//   - Nested JSON objects cause false-positive key matches
//   - Field ordering differs between serialisation passes

var (
	beginScript = redis.NewScript(`
-- TryBegin: atomically check and set an idempotency record.
-- KEYS[1]  = redis key
-- ARGV[1]  = serialised record JSON (new processing record)
-- ARGV[2]  = processing TTL in seconds
-- ARGV[3]  = fingerprint of the incoming request
--
-- Returns a table: {status, existing_record_json (optional)}

local key     = KEYS[1]
local record  = ARGV[1]
local ttl     = tonumber(ARGV[2])
local newFp   = ARGV[3]

local existing = redis.call("GET", key)

if not existing then
  redis.call("SETEX", key, ttl, record)
  return {"acquired", record}
end

local cjson = require("cjson")
local obj   = cjson.decode(existing)

if obj.fingerprint ~= newFp then
  return {"conflict", existing}
end

if obj.status == "completed" then
  return {"replay", existing}
end

if obj.status == "failed" then
  return {"failed", existing}
end

return {"in_progress", existing}
`)

	commitScript = redis.NewScript(`
-- Commit: atomically transition a record from processing to a final state.
-- KEYS[1] = redis key
-- ARGV[1] = owner (must match existing)
-- ARGV[2] = fingerprint (must match existing)
-- ARGV[3] = serialised updated record JSON (completed or failed)
-- ARGV[4] = new TTL in seconds

local key     = KEYS[1]
local owner   = ARGV[1]
local fp      = ARGV[2]
local record  = ARGV[3]
local ttl     = tonumber(ARGV[4])

local existing = redis.call("GET", key)
if not existing then
  return {"missing"}
end

local cjson = require("cjson")
local obj   = cjson.decode(existing)

if obj.owner ~= owner then
  return {"owner_mismatch"}
end

if obj.fingerprint ~= fp then
  return {"conflict"}
end

if obj.status ~= "processing" then
  return {"invalid_state", obj.status or "unknown"}
end

redis.call("SETEX", key, ttl, record)
return {"committed"}
`)

	abortScript = redis.NewScript(`
-- Abort: atomically handle abort based on failure mode.
-- KEYS[1] = redis key
-- ARGV[1] = owner
-- ARGV[2] = mode ("delete", "cache", "keep_processing_until_ttl")
-- ARGV[3] = serialised failed record JSON (only for cache mode)
-- ARGV[4] = failed TTL in seconds (only for cache mode)

local key     = KEYS[1]
local owner   = ARGV[1]
local mode    = ARGV[2]

local existing = redis.call("GET", key)
if not existing then
  return {"ok"}
end

local cjson = require("cjson")
local obj   = cjson.decode(existing)

if obj.owner ~= owner then
  return {"owner_mismatch"}
end

if obj.status ~= "processing" then
  return {"invalid_state", obj.status or "unknown"}
end

if mode == "delete" then
  redis.call("DEL", key)
  return {"deleted"}
end

if mode == "keep_processing_until_ttl" then
  -- Let the existing TTL expire naturally.
  return {"ok"}
end

if mode == "cache" then
  local record = ARGV[3]
  local ttl    = tonumber(ARGV[4])
  if record and ttl then
    redis.call("SETEX", key, ttl, record)
  end
  return {"cached"}
end

return {"ok"}
`)

	renewScript = redis.NewScript(`
-- Renew: extend the TTL of a processing record.
-- KEYS[1] = redis key
-- ARGV[1] = owner
-- ARGV[2] = TTL in seconds

local key   = KEYS[1]
local owner = ARGV[1]
local ttl   = tonumber(ARGV[2])

local existing = redis.call("GET", key)
if not existing then
  return {"missing"}
end

local cjson = require("cjson")
local obj   = cjson.decode(existing)

if obj.owner ~= owner then
  return {"owner_mismatch"}
end

if obj.status ~= "processing" then
  return {"ok"} -- not an error; just nothing to renew
end

redis.call("EXPIRE", key, ttl)
return {"renewed"}
`)
)

// Lua script return constants used by the repository to interpret results.
const (
	luaAcquired      = "acquired"
	luaConflict      = "conflict"
	luaReplay        = "replay"
	luaInProgress    = "in_progress"
	luaFailed        = "failed"
	luaCommitted     = "committed"
	luaDeleted       = "deleted"
	luaCached        = "cached"
	luaRenewed       = "renewed"
	luaMissing       = "missing"
	luaOwnerMismatch = "owner_mismatch"
	luaInvalidState  = "invalid_state"
)

// luaResult is the parsed first element of a Lua script return value.
func luaResult(v any) string {
	sl, ok := v.([]any)
	if !ok || len(sl) == 0 {
		return ""
	}
	s, ok := sl[0].(string)
	if !ok {
		return ""
	}
	return s
}

// luaPayload returns the second element of a Lua script return value, if any.
func luaPayload(v any) string {
	sl, ok := v.([]any)
	if !ok || len(sl) < 2 {
		return ""
	}
	s, ok := sl[1].(string)
	if !ok {
		return ""
	}
	return s
}
