package social

import (
	"context"
	"errors"
	"feedsystem/internal/account"
)

type SocialService struct {
	repo        *SocialRepository
	accountRepo *account.AccountRepository
}

func NewSocialService(repo *SocialRepository, accountRepo *account.AccountRepository) *SocialService {
	return &SocialService{repo: repo, accountRepo: accountRepo}
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

	return s.repo.Follow(ctx, social)
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

	return s.repo.Unfollow(ctx, social)
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
