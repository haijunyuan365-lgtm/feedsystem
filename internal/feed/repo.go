package feed

import (
	"context"
	"feedsystem/internal/social"
	"feedsystem/internal/video"
	"time"

	"gorm.io/gorm"
)

type FeedRepository struct {
	db *gorm.DB
}

func NewFeedRepository(db *gorm.DB) *FeedRepository {
	return &FeedRepository{db: db}
}

func (repo *FeedRepository) ListLatest(ctx context.Context, limit int, latestBefore time.Time) ([]*video.Video, error) {
	var videos []*video.Video

	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("create_time DESC")

	if !latestBefore.IsZero() {
		query = query.Where("create_time < ?", latestBefore)
	}

	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}

	return videos, nil
}

func (repo *FeedRepository) ListByFollowing(ctx context.Context, limit int, viewerAccountID uint, latestBefore time.Time) ([]*video.Video, error) {
	var videos []*video.Video

	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("create_time DESC")

	if viewerAccountID > 0 {
		followingSubQuery := repo.db.WithContext(ctx).
			Model(&social.Social{}).
			Select("vlogger_id").
			Where("follower_id = ?", viewerAccountID)

		query = query.Where("author_id IN (?)", followingSubQuery)
	}

	if !latestBefore.IsZero() {
		query = query.Where("create_time < ?", latestBefore)
	}

	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}

	return videos, nil
}

func (repo *FeedRepository) ListLikesCountWithCursor(ctx context.Context, limit int, cursor *LikesCountCursor) ([]*video.Video, error) {
	var videos []*video.Video

	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("likes_count DESC, id DESC")

	if cursor != nil {
		query = query.Where(
			"(likes_count < ?) OR (likes_count = ? AND id < ?)",
			cursor.LikesCount,
			cursor.LikesCount,
			cursor.ID,
		)
	}

	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}

	return videos, nil
}

func (repo *FeedRepository) ListByTag(ctx context.Context, tagName string, limit int) ([]*video.Video, error) {
	var videos []*video.Video

	err := repo.db.WithContext(ctx).Model(&video.Video{}).Table("videos").
		Joins("JOIN video_tags ON video_tags.video_id = videos.id").
		Joins("JOIN tags ON tags.id = video_tags.tag_id").
		Where("tags.name = ?", tagName).
		Order("videos.create_time DESC").
		Limit(limit).
		Find(&videos).Error

	return videos, err
}

func (repo *FeedRepository) ListByPopularity(ctx context.Context, limit int, cursor *PopularityCursor) ([]*video.Video, error) {
	var videos []*video.Video

	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("popularity DESC, create_time DESC, id DESC")

	//说明不是第一页
	if cursor != nil {
		query = query.Where(
			"(popularity < ?) OR (popularity = ? AND create_time < ?) OR (popularity = ? AND create_time = ? AND id < ?)",
			cursor.Popularity,
			cursor.Popularity, cursor.CreateTime,
			cursor.Popularity, cursor.CreateTime, cursor.ID,
		)
	}

	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}

	return videos, nil
}

// redis的zset只存video_id，从zset里面拿到的Video_ids需要批量查询出每个的视频详情
func (repo *FeedRepository) GetByIDs(ctx context.Context, ids []uint) ([]*video.Video, error) {
	var videos []*video.Video
	if len(ids) == 0 {
		return videos, nil
	}
	if err := repo.db.WithContext(ctx).Model(&video.Video{}).
		Where("id IN ?", ids).
		Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}
