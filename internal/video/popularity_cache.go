package video

import (
	"context"
	"strconv"
	"time"

	rediscache "feedsystem/internal/middleware/redis"
)

func UpdatePopularityCache(ctx context.Context, cache *rediscache.Client, id uint, change int64) {
	if cache == nil || id == 0 || change == 0 {
		return
	}

	_ = cache.Del(context.Background(), cache.Key("video:detail:id=%d", id))
	_ = cache.Del(context.Background(), cache.Key("video:entity:%d", id))

	// 在 Redis 里记录“这个视频在当前这一分钟热度变化了多少”，方便后面统计热视频排行榜。
	now := time.Now().UTC().Truncate(time.Minute)
	windowKey := cache.Key("hot:video:1m:%s", now.Format("200601021504"))
	member := strconv.FormatUint(uint64(id), 10)

	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_ = cache.ZincrBy(opCtx, windowKey, member, float64(change))
	_ = cache.Expire(opCtx, windowKey, 2*time.Hour)
}
