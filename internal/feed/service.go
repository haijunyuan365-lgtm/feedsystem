package feed

import (
	"context"
	rediscache "feedsystem/internal/middleware/redis"
	"feedsystem/internal/video"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type FeedService struct {
	repo     *FeedRepository
	likeRepo *video.LikeRepository
	cache    *rediscache.Client
	cacheTTL time.Duration
}

func NewFeedService(repo *FeedRepository, likeRepo *video.LikeRepository, cache *rediscache.Client) *FeedService {
	return &FeedService{repo: repo, likeRepo: likeRepo, cache: cache, cacheTTL: 24 * time.Hour}
}

func (f *FeedService) ListLatest(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListLatestResponse, error) {
	if f.cache == nil {
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}

	zsetKey := f.cache.Key("feed:global_timeline")
	zsetTail, err := f.cache.ZRangeWithScores(ctx, zsetKey, 0, 0)
	if err != nil {
		log.Printf("get global timeline zset tail failed: err=%v", err)
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}

	if len(zsetTail) == 0 {
		if err := f.rebuildGlobalTimeline(ctx); err != nil {
			log.Printf("rebuild global timeline failed: err=%v", err)
			return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
		}
	}

	maxScore := "+inf"
	if !latestBefore.IsZero() {
		maxScore = fmt.Sprintf("%d", latestBefore.UnixMilli()-1)
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	videoIDStrs, err := f.cache.ZRevRangeByScore(cacheCtx, zsetKey, maxScore, "-inf", 0, int64(limit))
	cancel()
	if err != nil {
		log.Printf("get latest videos from zset failed: err=%v", err)
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}
	if len(videoIDStrs) == 0 {
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}

	videoIDs := parseVideoIDs(videoIDStrs)
	videos, err := f.repo.GetByIDs(ctx, videoIDs)
	if err != nil {
		return ListLatestResponse{}, err
	}
	//因为 SQL不一定按照你传入的 ID 顺序返回，所以需要重新排列
	videos = buildOrderedVideoResult(videoIDs, videos)

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLatestResponse{}, err
	}

	var nextTime int64
	if len(videos) > 0 {
		nextTime = videos[len(videos)-1].CreateTime.UnixMilli()
	}

	return ListLatestResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   len(videos) == limit,
	}, nil
}

func (f *FeedService) listLatestFromDB(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListLatestResponse, error) {
	videos, err := f.repo.ListLatest(ctx, limit, latestBefore)
	if err != nil {
		return ListLatestResponse{}, err
	}

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLatestResponse{}, err
	}

	var nextTime int64
	if len(videos) > 0 {
		nextTime = videos[len(videos)-1].CreateTime.UnixMilli()
	}

	return ListLatestResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   len(videos) == limit,
	}, nil
}

func (f *FeedService) ListByFollowing(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListByFollowingResponse, error) {
	videos, err := f.repo.ListByFollowing(ctx, limit, viewerAccountID, latestBefore)
	if err != nil {
		return ListByFollowingResponse{}, err
	}

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByFollowingResponse{}, err
	}

	var nextTime int64
	if len(videos) > 0 {
		nextTime = videos[len(videos)-1].CreateTime.UnixMilli()
	}

	return ListByFollowingResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   len(videos) == limit,
	}, nil
}

func (f *FeedService) ListLikesCount(ctx context.Context, limit int, cursor *LikesCountCursor, viewerAccountID uint) (ListLikesCountResponse, error) {
	videos, err := f.repo.ListLikesCountWithCursor(ctx, limit, cursor)
	if err != nil {
		return ListLikesCountResponse{}, err
	}

	hasMore := len(videos) == limit

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLikesCountResponse{}, err
	}

	resp := ListLikesCountResponse{
		VideoList: feedVideos,
		HasMore:   hasMore,
	}

	if len(videos) > 0 {
		last := videos[len(videos)-1]

		nextLikesCountBefore := last.LikesCount
		nextIDBefore := last.ID

		resp.NextLikesCountBefore = &nextLikesCountBefore
		resp.NextIDBefore = &nextIDBefore
	}

	return resp, nil
}

func (f *FeedService) ListByTag(ctx context.Context, tagName string, limit int, viewerAccountID uint) (ListByTagResponse, error) {
	videos, err := f.repo.ListByTag(ctx, tagName, limit)
	if err != nil {
		return ListByTagResponse{}, err
	}

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByTagResponse{}, err
	}

	return ListByTagResponse{VideoList: feedVideos}, nil
}

func (f *FeedService) ListByPopularity(ctx context.Context, limit int, reqAsOf int64, offset int, viewerAccountID uint, latestPopularity int64, latestBefore time.Time, latestIDBefore uint) (ListByPopularityResponse, error) {
	if f.cache != nil {
		if resp, ok, err := f.listPopularityFromRedis(ctx, limit, reqAsOf, offset, viewerAccountID); err != nil {
			return ListByPopularityResponse{}, err
		} else if ok {
			return resp, nil
		}
	}

	var cursor *PopularityCursor
	if !latestBefore.IsZero() && latestIDBefore > 0 {
		cursor = &PopularityCursor{
			Popularity: latestPopularity,
			CreateTime: latestBefore,
			ID:         latestIDBefore,
		}
	}
	return f.listPopularityFromDB(ctx, limit, cursor, viewerAccountID)
}

func (f *FeedService) listPopularityFromRedis(ctx context.Context, limit int, reqAsOf int64, offset int, viewerAccountID uint) (ListByPopularityResponse, bool, error) {
	asOf := time.Now().UTC().Truncate(time.Minute)
	if reqAsOf > 0 {
		asOf = time.Unix(reqAsOf, 0).UTC().Truncate(time.Minute)
	}

	const win = 60
	keys := make([]string, 0, win)
	for i := 0; i < win; i++ {
		keys = append(keys, f.cache.Key("hot:video:1m:%s", asOf.Add(-time.Duration(i)*time.Minute).Format("200601021504")))
	}

	dest := f.cache.Key("hot:video:merge:1m:%s", asOf.Format("200601021504"))
	cacheCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()

	exists, _ := f.cache.Exists(cacheCtx, dest)
	if !exists {
		_ = f.cache.ZUnionStore(cacheCtx, dest, keys, "SUM")
		_ = f.cache.Expire(cacheCtx, dest, 2*time.Minute)
	}

	start := int64(offset)
	stop := start + int64(limit) - 1
	videoIDStrs, err := f.cache.ZRevRange(cacheCtx, dest, start, stop)
	if err != nil {
		log.Printf("get popularity zset failed: err=%v", err)
		return ListByPopularityResponse{}, false, nil
	}
	if len(videoIDStrs) == 0 {
		//翻页翻到底了
		if offset > 0 {
			return ListByPopularityResponse{
				VideoList:  []FeedVideoItem{},
				AsOf:       asOf.Unix(),
				NextOffset: offset,
				HasMore:    false,
			}, true, nil
		}
		return ListByPopularityResponse{}, false, nil
	}

	videoIDs := parseVideoIDs(videoIDStrs)
	videos, err := f.repo.GetByIDs(ctx, videoIDs)
	if err != nil {
		return ListByPopularityResponse{}, false, err
	}
	videos = buildOrderedVideoResult(videoIDs, videos)

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByPopularityResponse{}, false, err
	}

	resp := ListByPopularityResponse{
		VideoList:  feedVideos,
		AsOf:       asOf.Unix(),
		NextOffset: offset + len(feedVideos),
		HasMore:    len(feedVideos) == limit,
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextPopularity := last.Popularity
		nextBefore := last.CreateTime
		nextID := last.ID

		resp.NextLatestPopularity = &nextPopularity
		resp.NextLatestBefore = &nextBefore
		resp.NextLatestIDBefore = &nextID
	}

	return resp, true, nil
}

func (f *FeedService) listPopularityFromDB(ctx context.Context, limit int, cursor *PopularityCursor, viewerAccountID uint) (ListByPopularityResponse, error) {
	videos, err := f.repo.ListByPopularity(ctx, limit, cursor)
	if err != nil {
		return ListByPopularityResponse{}, err
	}

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByPopularityResponse{}, err
	}

	resp := ListByPopularityResponse{
		VideoList:  feedVideos,
		AsOf:       0,
		NextOffset: 0,
		HasMore:    len(videos) == limit,
	}

	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextPopularity := last.Popularity
		nextBefore := last.CreateTime
		nextID := last.ID

		resp.NextLatestPopularity = &nextPopularity
		resp.NextLatestBefore = &nextBefore
		resp.NextLatestIDBefore = &nextID
	}

	return resp, nil
}

func (f *FeedService) buildFeedVideos(ctx context.Context, videos []*video.Video, viewerAccountID uint) ([]FeedVideoItem, error) {
	feedVideos := make([]FeedVideoItem, 0, len(videos))

	videoIDs := make([]uint, 0, len(videos))
	for _, v := range videos {
		videoIDs = append(videoIDs, v.ID)
	}

	likedMap, err := f.getLikedMap(ctx, videoIDs, viewerAccountID)
	if err != nil {
		return nil, err
	}

	for _, v := range videos {
		feedVideos = append(feedVideos, FeedVideoItem{
			ID: v.ID,
			Author: FeedAuthor{
				ID:       v.AuthorID,
				Username: v.Username,
			},
			Title:       v.Title,
			Description: v.Description,
			PlayURL:     v.PlayURL,
			CoverURL:    v.CoverURL,
			CreateTime:  v.CreateTime.UnixMilli(),
			LikesCount:  v.LikesCount,
			//Go 里 map 查不到 key 时，bool 默认值就是 false，所以这里不用额外判断。
			IsLiked: likedMap[v.ID],
		})
	}

	return feedVideos, nil
}

func (f *FeedService) getLikedMap(ctx context.Context, videoIDs []uint, viewerAccountID uint) (map[uint]bool, error) {
	likedMap := make(map[uint]bool)
	if len(videoIDs) == 0 || viewerAccountID == 0 {
		return likedMap, nil
	}

	missedVideoIDs := videoIDs
	if f.cache != nil {
		cacheKeys := make([]string, 0, len(videoIDs))
		for _, videoID := range videoIDs {
			cacheKeys = append(cacheKeys, f.likeCacheKey(videoID, viewerAccountID))
		}

		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		results, err := f.cache.MGet(cacheCtx, cacheKeys...)
		cancel()

		if err == nil {
			missedVideoIDs = make([]uint, 0)
			for i, result := range results {
				videoID := videoIDs[i]
				if result == nil {
					missedVideoIDs = append(missedVideoIDs, videoID)
					continue
				}
				if value, ok := result.(string); ok && value == "1" {
					likedMap[videoID] = true
					continue
				}
				missedVideoIDs = append(missedVideoIDs, videoID)
			}
		} else {
			log.Printf("mget like cache failed: err=%v", err)
		}
	}

	if len(missedVideoIDs) == 0 || f.likeRepo == nil {
		return likedMap, nil
	}

	dbLikedMap, err := f.likeRepo.BatchGetLiked(ctx, missedVideoIDs, viewerAccountID)
	if err != nil {
		return nil, err
	}

	for videoID, isLiked := range dbLikedMap {
		likedMap[videoID] = isLiked
		if isLiked {
			f.setLikeCache(ctx, videoID, viewerAccountID)
		}
	}

	return likedMap, nil
}

func (f *FeedService) setLikeCache(ctx context.Context, videoID, accountID uint) {
	if f.cache == nil || videoID == 0 || accountID == 0 {
		return
	}

	cacheKey := f.likeCacheKey(videoID, accountID)
	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	if err := f.cache.SetBytes(cacheCtx, cacheKey, []byte("1"), f.cacheTTL); err != nil {
		log.Printf("set feed like cache failed: key=%s err=%v", cacheKey, err)
	}
}

func (f *FeedService) likeCacheKey(videoID, accountID uint) string {
	return f.cache.Key("like:video_id=%d:account_id=%d", videoID, accountID)
}

func (f *FeedService) rebuildGlobalTimeline(ctx context.Context) error {
	if f.cache == nil {
		return nil
	}

	videos, err := f.repo.ListLatest(ctx, 1000, time.Time{})
	if err != nil {
		return err
	}
	if len(videos) == 0 {
		return nil
	}

	members := make([]redis.Z, 0, len(videos))
	for _, v := range videos {
		members = append(members, redis.Z{
			Score:  float64(v.CreateTime.UnixMilli()),
			Member: strconv.FormatUint(uint64(v.ID), 10),
		})
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	return f.cache.ZAdd(cacheCtx, f.cache.Key("feed:global_timeline"), members...)
}

func parseVideoIDs(idStrs []string) []uint {
	ids := make([]uint, 0, len(idStrs))
	for _, idStr := range idStrs {
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil || id == 0 {
			continue
		}
		ids = append(ids, uint(id))
	}
	return ids
}

func buildOrderedVideoResult(orderedIDs []uint, videos []*video.Video) []*video.Video {
	videoMap := make(map[uint]*video.Video, len(videos))
	for _, v := range videos {
		videoMap[v.ID] = v
	}

	orderedVideos := make([]*video.Video, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		if v := videoMap[id]; v != nil {
			orderedVideos = append(orderedVideos, v)
		}
	}
	return orderedVideos
}
