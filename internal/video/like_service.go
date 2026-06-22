package video

import (
	"context"
	"errors"
	rediscache "feedsystem/internal/middleware/redis"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type LikeService struct {
	repo      *LikeRepository
	VideoRepo *VideoRepository
	cache     *rediscache.Client
}

func NewLikeService(repo *LikeRepository, videoRepo *VideoRepository, cache *rediscache.Client) *LikeService {
	return &LikeService{repo: repo, VideoRepo: videoRepo, cache: cache}
}
func isDupKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

func (s *LikeService) Like(ctx context.Context, like *Like) error {
	if like == nil {
		return errors.New("like is nil")
	}
	if like.VideoID == 0 || like.AccountID == 0 {
		return errors.New("video_id and account_id are required")
	}

	if s.VideoRepo != nil {
		ok, err := s.VideoRepo.IsExist(ctx, like.VideoID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("video not found")
		}
	}

	isLiked, err := s.repo.IsLiked(ctx, like.VideoID, like.AccountID)
	if err != nil {
		return err
	}
	if isLiked {
		return errors.New("user has liked this video")
	}

	like.CreatedAt = time.Now()

	err = s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		//事务里再次检查视频存在  我只需要确认 id 存在，不需要把 title、play_url、cover_url 全部查出来
		if err := tx.Select("id").First(&Video{}, like.VideoID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("video not found")
			}
			return err
		}

		if err := tx.Create(like).Error; err != nil {
			if isDupKey(err) {
				return errors.New("user has liked this video")
			}
			return err
		}
		//类似 UPDATE videos SET likes_count = likes_count + 1 WHERE id = ?
		// gorm.Expr("likes_count + 1") 数据库原子更新，更适合计数字段
		if err := tx.Model(&Video{}).Where("id = ?", like.VideoID).
			UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error; err != nil {
			return err
		}
		return tx.Model(&Video{}).Where("id = ?", like.VideoID).
			UpdateColumn("popularity", gorm.Expr("popularity + 3")).Error
	})
	if err != nil {
		return err
	}
	//因为只有 MySQL 真的点赞成功后，Redis 才能写
	s.setLikeCache(ctx, like.VideoID, like.AccountID)
	UpdatePopularityCache(ctx, s.cache, like.VideoID, 3)
	return nil
}

func (s *LikeService) Unlike(ctx context.Context, like *Like) error {
	if like == nil {
		return errors.New("like is nil")
	}
	if like.VideoID == 0 || like.AccountID == 0 {
		return errors.New("video_id and account_id are required")
	}

	if s.VideoRepo != nil {
		ok, err := s.VideoRepo.IsExist(ctx, like.VideoID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("video not found")
		}
	}

	isLiked, err := s.repo.IsLiked(ctx, like.VideoID, like.AccountID)
	if err != nil {
		return err
	}
	if !isLiked {
		return errors.New("user has not liked this video")
	}

	err = s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		del := tx.Where("video_id = ? AND account_id = ?", like.VideoID, like.AccountID).Delete(&Like{})
		if del.Error != nil {
			return del.Error
		}
		if del.RowsAffected == 0 {
			return errors.New("user has not liked this video")
		}
		//GREATEST(a, b) 是取较大值  防止点赞数变成负数。
		if err := tx.Model(&Video{}).Where("id = ?", like.VideoID).
			UpdateColumn("likes_count", gorm.Expr("GREATEST(likes_count - 1, 0)")).Error; err != nil {
			return err
		}
		return tx.Model(&Video{}).Where("id = ?", like.VideoID).
			UpdateColumn("popularity", gorm.Expr("GREATEST(popularity - 3, 0)")).Error
	})
	if err != nil {
		return err
	}
	//只缓存点赞成功状态， 取消点赞后，这个状态已经不成立了，所以直接删除 key
	s.deleteLikeCache(ctx, like.VideoID, like.AccountID)
	UpdatePopularityCache(ctx, s.cache, like.VideoID, -3)
	return nil
}

func (s *LikeService) IsLiked(ctx context.Context, videoID, accountID uint) (bool, error) {
	if videoID == 0 || accountID == 0 {
		return false, nil
	}

	if s.cache != nil {
		cacheKey := s.likeCacheKey(videoID, accountID)
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		b, err := s.cache.GetBytes(cacheCtx, cacheKey)
		cancel()
		//只存已点赞状态，只要查到了说明是已点赞的
		if err == nil && string(b) == "1" {
			return true, nil
		}
		if err != nil && !rediscache.IsMiss(err) {
			log.Printf("get like cache failed: key=%s err=%v", cacheKey, err)
		}
	}

	isLiked, err := s.repo.IsLiked(ctx, videoID, accountID)
	if err != nil {
		return false, err
	}
	if isLiked {
		s.setLikeCache(ctx, videoID, accountID)
	}
	return isLiked, nil
}

func (s *LikeService) ListLikedVideos(ctx context.Context, accountID uint) ([]Video, error) {
	return s.repo.ListLikedVideos(ctx, accountID)
}

func (s *LikeService) setLikeCache(ctx context.Context, videoID, accountID uint) {
	if s.cache == nil || videoID == 0 || accountID == 0 {
		return
	}

	cacheKey := s.likeCacheKey(videoID, accountID)
	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	if err := s.cache.SetBytes(cacheCtx, cacheKey, []byte("1"), 24*time.Hour); err != nil {
		log.Printf("set like cache failed: key=%s err=%v", cacheKey, err)
	}
}

func (s *LikeService) deleteLikeCache(ctx context.Context, videoID, accountID uint) {
	if s.cache == nil || videoID == 0 || accountID == 0 {
		return
	}

	cacheKey := s.likeCacheKey(videoID, accountID)
	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	if err := s.cache.Del(cacheCtx, cacheKey); err != nil {
		log.Printf("delete like cache failed: key=%s err=%v", cacheKey, err)
	}
}

func (s *LikeService) likeCacheKey(videoID, accountID uint) string {
	return s.cache.Key("like:video_id=%d:account_id=%d", videoID, accountID)
}
