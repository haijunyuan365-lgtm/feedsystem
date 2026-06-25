package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	rediscache "feedsystem/internal/middleware/redis"
	"feedsystem/internal/video"

	localcache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

type FeedService struct {
	repo         *FeedRepository
	likeRepo     *video.LikeRepository
	cache        *rediscache.Client
	localcache   *localcache.Cache
	cacheTTL     time.Duration
	requestGroup singleflight.Group
}

func NewFeedService(repo *FeedRepository, likeRepo *video.LikeRepository, cacheClient *rediscache.Client) *FeedService {
	return &FeedService{
		repo:       repo,
		likeRepo:   likeRepo,
		cache:      cacheClient,
		localcache: localcache.New(3*time.Second, 5*time.Second),
		cacheTTL:   24 * time.Hour,
	}
}

func (f *FeedService) GetVideoByIDs(ctx context.Context, videoIDs []uint) ([]*video.Video, error) {
	if len(videoIDs) == 0 {
		return []*video.Video{}, nil
	}
	if f.cache == nil {
		videos, err := f.repo.GetByIDs(ctx, videoIDs)
		if err != nil {
			return nil, err
		}
		return buildOrderedVideoResult(videoIDs, videos), nil
	}

	videoMap := make(map[uint]*video.Video)
	var missedL1 []uint

	for _, id := range videoIDs {
		cacheKey := f.cache.Key("video:entity:%d", id)
		if f.localcache != nil {
			if v, found := f.localcache.Get(cacheKey); found {
				if data, ok := v.(video.Video); ok {
					videoMap[id] = &data
					continue
				}
			}
		}
		missedL1 = append(missedL1, id)
	}

	if len(missedL1) == 0 {
		return buildOrderedResult(videoIDs, videoMap), nil
	}

	var missedL2 []uint
	cacheKeys := make([]string, len(missedL1))
	for i, id := range missedL1 {
		cacheKeys[i] = f.cache.Key("video:entity:%d", id)
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	results, err := f.cache.MGet(cacheCtx, cacheKeys...)
	cancel()

	if err == nil {
		for i, res := range results {
			id := missedL1[i]
			if res != nil {
				if str, ok := res.(string); ok {
					var v video.Video
					if err := json.Unmarshal([]byte(str), &v); err == nil {
						videoMap[id] = &v
						if f.localcache != nil {
							f.localcache.Set(cacheKeys[i], v, 5*time.Second)
						}
						continue
					}
				}
			}
			missedL2 = append(missedL2, id)
		}
	} else {
		log.Printf("L2 Redis MGet 失败，全部降级到 MySQL: %v", err)
		missedL2 = missedL1
	}

	if len(missedL2) == 0 {
		return buildOrderedResult(videoIDs, videoMap), nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, id := range missedL2 {
		wg.Add(1)
		go func(videoID uint) {
			defer wg.Done()

			sfKey := f.cache.Key("sf:entity:%d", videoID)
			//f.requestGroup.Do(key, fn)
			//如果很多 goroutine 同时请求同一个 key，只有第一个会真的执行 fn，其他会等它执行完，直接拿同一份结果
			v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
				videoList, err := f.repo.GetByIDs(ctx, []uint{videoID})
				if err != nil || len(videoList) == 0 {
					return nil, err
				}

				safeCopy := *videoList[0]
				//回写入redis
				cacheKey := f.cache.Key("video:entity:%d", safeCopy.ID)
				if b, err := json.Marshal(safeCopy); err == nil {
					go func(k string, b []byte) {
						setCtx, setCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
						defer setCancel()
						_ = f.cache.SetBytes(setCtx, k, b, time.Hour)
					}(cacheKey, b)
				}
				return videoList[0], nil
			})

			if err == nil && v != nil {
				safeCopy := *(v.(*video.Video))
				mu.Lock()
				videoMap[videoID] = &safeCopy
				mu.Unlock()
				//回写入localcache
				if f.localcache != nil {
					f.localcache.Set(f.cache.Key("video:entity:%d", safeCopy.ID), safeCopy, 5*time.Second)
				}
			}
		}(id)
	}
	wg.Wait()

	return buildOrderedResult(videoIDs, videoMap), nil
}

func (f *FeedService) ListLatest(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListLatestResponse, error) {
	if f.cache == nil {
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}

	timelineKey := f.cache.Key("feed:global_timeline")
	zsetTail, err := f.cache.ZRangeWithScores(ctx, timelineKey, 0, 0)
	if err != nil {
		log.Printf("get global timeline zset tail failed: err=%v", err)
		return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
	}

	if len(zsetTail) == 0 {
		sfKey := f.cache.Key("sf:fallback:global_timeline_rebuild")
		//作用：避免很多请求同时发现缓存空了，然后一起冲 DB 重建，造成雪崩。
		v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
			dbVideos, err := f.repo.ListLatest(ctx, 1000, time.Time{})
			if err != nil {
				return nil, err
			}
			if len(dbVideos) == 0 {
				return "EMPTY_DB", nil
			}

			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			members := make([]redis.Z, 0, len(dbVideos))
			for _, vid := range dbVideos {
				members = append(members, redis.Z{
					Score:  float64(vid.CreateTime.UnixMilli()),
					Member: strconv.FormatUint(uint64(vid.ID), 10),
				})
			}
			if err := f.cache.ZAdd(bgCtx, timelineKey, members...); err != nil {
				return nil, err
			}
			return "SUCCESS", nil
		})
		if err != nil {
			log.Printf("rebuild global timeline failed: err=%v", err)
			return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
		}
		if v == "EMPTY_DB" {
			return ListLatestResponse{VideoList: []FeedVideoItem{}, HasMore: false}, nil
		}
		//缓存已经被重建好了，重新走一遍当前方法。这次就能命中 Redis 热路径了。这是个很自然的“重试进入正常流程”
		return f.ListLatest(ctx, limit, latestBefore, viewerAccountID)
	}

	watermark := int64(zsetTail[0].Score)
	reqTime := time.Now().UnixMilli()
	if !latestBefore.IsZero() {
		reqTime = latestBefore.UnixMilli()
	}

	var baseVideos []*video.Video
	//如果请求时间已经不新了，甚至比缓存里最老的数据还老
	//说明 Redis 这批热数据帮不上忙，直接走冷数据查询更合适
	if reqTime <= watermark {
		sfKey := f.cache.Key("sf:cold:listLatest:%d:%d", limit, reqTime)
		v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
			return f.repo.ListLatest(ctx, limit, latestBefore)
		})
		if err != nil {
			return ListLatestResponse{}, err
		}
		baseVideos = v.([]*video.Video)
	} else {
		//进入 Redis 热路径。说明请求时间落在缓存可覆盖范围内
		maxScore := "+inf"
		if !latestBefore.IsZero() {
			maxScore = fmt.Sprintf("%d", reqTime-1)
		}

		cacheCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		videoIDStrs, err := f.cache.ZRevRangeByScore(cacheCtx, timelineKey, maxScore, "-inf", 0, int64(limit))
		cancel()
		if err != nil {
			log.Printf("get latest videos from zset failed: err=%v", err)
			return f.listLatestFromDB(ctx, limit, latestBefore, viewerAccountID)
		}

		videoIDs := parseVideoIDs(videoIDStrs)
		if len(videoIDs) > 0 {
			baseVideos, err = f.GetVideoByIDs(ctx, videoIDs)
			if err != nil {
				return ListLatestResponse{}, err
			}
		}

		//Redis 里拿到的热数据不够一页。说明当前请求需要“热数据 + 冷数据”拼起来
		if len(baseVideos) < limit {
			remainLimit := limit - len(baseVideos)
			coldCursor := latestBefore
			if len(baseVideos) > 0 {
				coldCursor = baseVideos[len(baseVideos)-1].CreateTime
			}

			sfKey := f.cache.Key("sf:stitch:listLatest:%d:%d", remainLimit, coldCursor.UnixMilli())
			v, err, _ := f.requestGroup.Do(sfKey, func() (interface{}, error) {
				return f.repo.ListLatest(ctx, remainLimit, coldCursor)
			})
			if err == nil {
				baseVideos = append(baseVideos, v.([]*video.Video)...)
			}
		}
	}

	feedVideos, err := f.buildFeedVideos(ctx, baseVideos, viewerAccountID)
	if err != nil {
		return ListLatestResponse{}, err
	}

	var nextTime int64
	if len(baseVideos) > 0 {
		nextTime = baseVideos[len(baseVideos)-1].CreateTime.UnixMilli()
	}

	return ListLatestResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   len(baseVideos) == limit,
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

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLikesCountResponse{}, err
	}

	resp := ListLikesCountResponse{
		VideoList: feedVideos,
		HasMore:   len(videos) == limit,
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
	videos, err := f.GetVideoByIDs(ctx, videoIDs)
	if err != nil {
		return ListByPopularityResponse{}, false, err
	}

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
			IsLiked:     likedMap[v.ID],
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

func buildOrderedResult(orderedIDs []uint, dataMap map[uint]*video.Video) []*video.Video {
	res := make([]*video.Video, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		if v, exists := dataMap[id]; exists && v != nil {
			res = append(res, v)
		}
	}
	return res
}

func buildOrderedVideoResult(orderedIDs []uint, videos []*video.Video) []*video.Video {
	videoMap := make(map[uint]*video.Video, len(videos))
	for _, v := range videos {
		videoMap[v.ID] = v
	}
	return buildOrderedResult(orderedIDs, videoMap)
}
