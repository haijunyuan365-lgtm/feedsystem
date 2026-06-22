//关注表 Social

package social

import "feedsystem/internal/account"

// 关注关系表
type Social struct {
	ID uint `gorm:"primaryKey"`
	//使用联合唯一索引，同一个用户不能重复关注同一个人
	/*
		注意：
		1 关注 2
		2 关注 1
		这是两条不同关系，表示互关
	*/
	//关注者
	FollowerID uint `gorm:"not null;index:idx_social_follower;uniqueIndex:idx_social_follower_vlogger"`
	//被关注者
	VloggerID uint `gorm:"not null;index:idx_social_vlogger;uniqueIndex:idx_social_follower_vlogger"`
}

type FollowRequest struct {
	VloggerID uint `json:"vlogger_id"`
}

type UnfollowRequest struct {
	VloggerID uint `json:"vlogger_id"`
}

type GetAllFollowersRequest struct {
	VloggerID uint `json:"vlogger_id"`
}

type GetAllVloggersRequest struct {
	FollowerID uint `json:"follower_id"`
}

type GetAllFollowersResponse struct {
	Followers     []*account.Account `json:"followers"`
	FollowerCount int64              `json:"follower_count"`
}

type GetAllVloggersResponse struct {
	Vloggers     []*account.Account `json:"vloggers"`
	VloggerCount int64              `json:"vlogger_count"`
}

type SocialCounts struct {
	FollowerCount int64 `json:"follower_count"`
	VloggerCount  int64 `json:"vlogger_count"`
}
