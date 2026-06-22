package video

import (
	"context"
	"strconv"
	"time"

	rediscache "feedsystem/internal/middleware/redis"

	redis "github.com/redis/go-redis/v9"
)

const (
	GlobalTimelineKey = "feed:global_timeline"
	PopularZSetKey    = "video:popular:zset"
)

func AddToGlobalTimeline(ctx context.Context, cache *rediscache.Client, id uint, createTime time.Time) {
	if cache == nil || id == 0 || createTime.IsZero() {
		return
	}

	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_ = cache.ZAdd(opCtx, cache.Key(GlobalTimelineKey), redis.Z{
		Score:  float64(createTime.UnixMilli()),
		Member: strconv.FormatUint(uint64(id), 10),
	})

	// 全局时间线只保留最近 1000 条，避免 Redis 无限增长。 rank 默认是从低分到高分算的
	_ = cache.ZRemRangeByRank(opCtx, cache.Key(GlobalTimelineKey), 0, -1001)
}

func UpdatePopularityCache(ctx context.Context, cache *rediscache.Client, id uint, change int64) {
	if cache == nil || id == 0 || change == 0 {
		return
	}

	_ = cache.Del(context.Background(), cache.Key("video:detail:id=%d", id))
	_ = cache.Del(context.Background(), cache.Key("video:entity:%d", id))

	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	member := strconv.FormatUint(uint64(id), 10)
	_ = cache.ZincrBy(opCtx, cache.Key(PopularZSetKey), member, float64(change))

	// 热榜也只保留分数最高的 1000 个视频。
	_ = cache.ZRemRangeByRank(opCtx, cache.Key(PopularZSetKey), 0, -1001)
}
