package social

import (
	"context"
	"errors"
	"feedsystem/internal/account"
	"feedsystem/internal/middleware/rabbitmq"
	rediscache "feedsystem/internal/middleware/redis"
	"log"
)

type SocialService struct {
	repo        *SocialRepository
	accountRepo *account.AccountRepository
	socialMQ    *rabbitmq.SocialMQ
	cache       *rediscache.Client
}

func NewSocialService(repo *SocialRepository, accountRepo *account.AccountRepository, socialMQ *rabbitmq.SocialMQ, cache *rediscache.Client) *SocialService {
	return &SocialService{repo: repo, accountRepo: accountRepo, socialMQ: socialMQ, cache: cache}
}

func (s *SocialService) Follow(ctx context.Context, social *Social) error {
	if social == nil {
		return errors.New("social is nil")
	}
	if social.FollowerID == 0 || social.VloggerID == 0 {
		return errors.New("follower_id and vlogger_id are required")
	}

	_, err := s.accountRepo.FindByID(ctx, social.FollowerID)
	if err != nil {
		return err
	}
	_, err = s.accountRepo.FindByID(ctx, social.VloggerID)
	if err != nil {
		return err
	}
	if social.FollowerID == social.VloggerID {
		return errors.New("can not follow self")
	}

	isFollowed, err := s.repo.IsFollowed(ctx, social)
	if err != nil {
		return err
	}
	if isFollowed {
		return errors.New("already followed")
	}

	if err := s.repo.Follow(ctx, social); err != nil {
		return err
	}
	s.invalidateFollowingFeedCache(context.Background(), social.FollowerID)
	if s.socialMQ != nil {
		if err := s.socialMQ.Follow(ctx, social.FollowerID, social.VloggerID); err != nil {
			log.Printf("social MQ Follow 发布失败: %v", err)
		}
	}
	return nil
}

func (s *SocialService) Unfollow(ctx context.Context, social *Social) error {
	if social == nil {
		return errors.New("social is nil")
	}
	if social.FollowerID == 0 || social.VloggerID == 0 {
		return errors.New("follower_id and vlogger_id are required")
	}

	_, err := s.accountRepo.FindByID(ctx, social.FollowerID)
	if err != nil {
		return err
	}
	_, err = s.accountRepo.FindByID(ctx, social.VloggerID)
	if err != nil {
		return err
	}

	isFollowed, err := s.repo.IsFollowed(ctx, social)
	if err != nil {
		return err
	}
	if !isFollowed {
		return errors.New("not followed")
	}

	if err := s.repo.Unfollow(ctx, social); err != nil {
		return err
	}
	s.invalidateFollowingFeedCache(context.Background(), social.FollowerID)
	if s.socialMQ != nil {
		if err := s.socialMQ.UnFollow(ctx, social.FollowerID, social.VloggerID); err != nil {
			log.Printf("social MQ UnFollow 发布失败: %v", err)
		}
	}
	return nil
}

func (s *SocialService) GetAllFollowers(ctx context.Context, vloggerID uint) ([]*account.Account, error) {
	_, err := s.accountRepo.FindByID(ctx, vloggerID)
	if err != nil {
		return nil, err
	}
	return s.repo.GetAllFollowers(ctx, vloggerID)
}

func (s *SocialService) GetAllVloggers(ctx context.Context, followerID uint) ([]*account.Account, error) {
	_, err := s.accountRepo.FindByID(ctx, followerID)
	if err != nil {
		return nil, err
	}
	return s.repo.GetAllVloggers(ctx, followerID)
}

func (s *SocialService) CountFollowers(ctx context.Context, vloggerID uint) (int64, error) {
	return s.repo.CountFollowers(ctx, vloggerID)
}

func (s *SocialService) CountVloggers(ctx context.Context, followerID uint) (int64, error) {
	return s.repo.CountVloggers(ctx, followerID)
}

func (s *SocialService) IsFollowed(ctx context.Context, social *Social) (bool, error) {
	if social == nil {
		return false, errors.New("social is nil")
	}
	_, err := s.accountRepo.FindByID(ctx, social.FollowerID)
	if err != nil {
		return false, err
	}
	_, err = s.accountRepo.FindByID(ctx, social.VloggerID)
	if err != nil {
		return false, err
	}
	return s.repo.IsFollowed(ctx, social)
}

func (s *SocialService) invalidateFollowingFeedCache(ctx context.Context, accountID uint) {
	if s.cache == nil {
		return
	}
	pattern := s.cache.Key("feed:listByFollowing:*:accountID=%d:*", accountID)
	if err := s.cache.DelByPattern(ctx, pattern); err != nil {
		log.Printf("失效 Following 缓存失败: accountID=%d, err=%v", accountID, err)
	}
}
