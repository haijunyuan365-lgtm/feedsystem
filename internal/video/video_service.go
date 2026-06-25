package video

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem/internal/middleware/redis"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
)

type VideoService struct {
	repo  *VideoRepository
	cache *redis.Client
	//缓存过期时间
	cacheTTL time.Duration
}

func NewVideoService(repo *VideoRepository, cache *redis.Client) *VideoService {
	return &VideoService{repo: repo,
		cache:    cache,
		cacheTTL: 5 * time.Minute,
	}
}

func (vs *VideoService) Publish(ctx context.Context, video *Video) error {
	if video == nil {
		return errors.New("video is nil")
	}

	video.Title = strings.TrimSpace(video.Title)
	video.PlayURL = strings.TrimSpace(video.PlayURL)
	video.CoverURL = strings.TrimSpace(video.CoverURL)

	if video.Title == "" {
		return errors.New("title is required")
	}
	if video.PlayURL == "" {
		return errors.New("play url is required")
	}
	if video.CoverURL == "" {
		return errors.New("cover url is required")
	}
	//因为这里的发布视频不再是单表操作了，而是多表操作
	//这里在 service 层直接打开事务。这样做的原因是：事务本质上属于业务流程
	err := vs.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(video).Error; err != nil {
			return err
		}
		//最新流数据存入数据库，rabbitMQ异步从数据库取数据，再上传到redis中
		msg := OutboxMsg{
			VideoID:    video.ID,
			EventType:  "video_published",
			Status:     "pending",
			CreateTime: video.CreateTime,
		}
		if err := tx.Create(&msg).Error; err != nil {
			return err
		}

		tags := ExtractTags(video.Title + " " + video.Description)
		for _, tagName := range tags {
			var tag Tag
			if err := tx.Where("name = ?", tagName).FirstOrCreate(&tag, Tag{Name: tagName}).Error; err != nil {
				return err
			}
			if err := tx.Create(&VideoTag{VideoID: video.ID, TagID: tag.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (vs *VideoService) GetDetail(ctx context.Context, id uint) (*Video, error) {
	if id == 0 {
		return nil, errors.New("video id is required")
	}

	if vs.cache != nil {
		if cachedVideo, ok := vs.getVideoDetailFromCache(ctx, id); ok {
			return cachedVideo, nil
		}
	}

	video, err := vs.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if vs.cache != nil {
		vs.setVideoDetailCache(ctx, video)
	}

	return video, nil
}

func (vs *VideoService) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	return vs.repo.ListByAuthorID(ctx, int64(authorID))
}

func (vs *VideoService) getVideoDetailFromCache(ctx context.Context, id uint) (*Video, bool) {
	cacheKey := vs.videoDetailCacheKey(id)

	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	b, err := vs.cache.GetBytes(cacheCtx, cacheKey)
	if err != nil {
		//redis.IsMiss(err) 判断的是：这个 key 本来就不存在,这不算真正的错误,所以除了这个问题之外的才需要打印日志
		if !redis.IsMiss(err) {
			log.Printf("get video detail cache failed: key=%s err=%v", cacheKey, err)
		}
		return nil, false
	}

	var video Video
	if err := json.Unmarshal(b, &video); err != nil {
		log.Printf("unmarshal video detail cache failed: key=%s err=%v", cacheKey, err)
		return nil, false
	}

	return &video, true
}

func (vs *VideoService) setVideoDetailCache(ctx context.Context, video *Video) {
	if video == nil || vs.cache == nil {
		return
	}

	b, err := json.Marshal(video)
	if err != nil {
		log.Printf("marshal video detail cache failed: video_id=%d err=%v", video.ID, err)
		return
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	cacheKey := vs.videoDetailCacheKey(video.ID)
	if err := vs.cache.SetBytes(cacheCtx, cacheKey, b, vs.cacheTTL); err != nil {
		log.Printf("set video detail cache failed: key=%s err=%v", cacheKey, err)
	}
}

func (vs *VideoService) videoDetailCacheKey(id uint) string {
	return vs.cache.Key("video:detail:id=%d", id)
}
