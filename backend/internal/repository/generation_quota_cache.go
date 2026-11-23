package repository

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

// Applying the delta and advancing its cursor is one Redis operation. A cache
// miss needs no delta: its next seed reads DB usage and cursor atomically.
// Existing v1 hashes without IDs are upgraded without discarding ordinary
// increments. A newer quota identity always protects a deleted/recreated row.
const applyGenerationQuotaScript = quotaCursorLua + `
if redis.call("EXISTS",KEYS[1]) == 0 then return 1 end
if redis.call("HGET",KEYS[1],"schema_version") ~= ARGV[1] then return 1 end
local quotaID = redis.call("HGET",KEYS[1],"quota_id") or "0"
if intcmp(quotaID,ARGV[2]) > 0 then return 1 end
if quotaID ~= "0" and intcmp(quotaID,ARGV[2]) < 0 then
    redis.call("DEL",KEYS[1])
    return 1
end
local cursor = redis.call("HGET",KEYS[1],"generation_cursor") or "0"
if intcmp(cursor,ARGV[3]) >= 0 then return 1 end
local fields = {"daily", "weekly", "monthly"}
for i,prefix in ipairs(fields) do
    local start = tonumber(redis.call("HGET",KEYS[1],prefix.."_window_start"))
    local target = tonumber(ARGV[5+i])
    if start == nil then
        redis.call("HSET",KEYS[1],prefix.."_window_start",target)
        start = target
    elseif (i < 3 and target > start) or (i == 3 and target-start >= 2592000) then
        redis.call("HSET",KEYS[1],prefix.."_usage",0,prefix.."_window_start",target)
        start = target
    end
    if tonumber(ARGV[9]) >= start then
        redis.call("HINCRBYFLOAT",KEYS[1],prefix.."_usage",ARGV[4])
    end
end
redis.call("HSET",KEYS[1],"quota_id",ARGV[2],"generation_cursor",ARGV[3])
redis.call("HINCRBY",KEYS[1],"version",1)
redis.call("EXPIRE",KEYS[1],ARGV[5])
if ARGV[10] ~= "" then
    redis.call("SADD",KEYS[2],ARGV[10])
    redis.call("EXPIRE",KEYS[2],ARGV[11])
end
return 1
`

func (c *billingCache) ApplyGenerationQuota(ctx context.Context, event *service.GenerationQuotaEvent, ttl time.Duration, dirty bool) error {
	if event == nil || event.ID <= 0 || event.QuotaID <= 0 || event.UserID <= 0 || !domain.IsConcretePlatform(event.Platform) || ttl < time.Second || ttl > 365*24*time.Hour || event.CreatedAt.IsZero() || event.DailyStart.IsZero() || event.WeeklyStart.IsZero() || event.MonthlyStart.IsZero() || len(event.Amount) > 64 {
		return errors.New("invalid generation quota event")
	}
	amount, err := decimal.NewFromString(event.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -service.UsageBillingMonetaryScale || amount.GreaterThan(decimal.NewFromInt(1_000_000_000_000)) {
		return errors.New("invalid generation quota amount")
	}
	member := ""
	if dirty {
		member = userPlatformQuotaDirtyMember(event.UserID, event.Platform)
	}
	return c.rdb.Eval(ctx, applyGenerationQuotaScript, []string{userPlatformQuotaCacheKey(event.UserID, event.Platform), userPlatformQuotaDirtySetKey()},
		service.UserPlatformQuotaCacheSchemaV1, strconv.FormatInt(event.QuotaID, 10), strconv.FormatInt(event.ID, 10), amount.StringFixed(8), int64(ttl.Seconds()),
		event.DailyStart.Unix(), event.WeeklyStart.Unix(), event.MonthlyStart.Unix(), event.CreatedAt.Unix(), member, userPlatformQuotaDirtyTTLSeconds).Err()
}
