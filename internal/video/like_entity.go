package video

import "time"

// 点赞表
type Like struct {
	ID uint `gorm:"primaryKey" json:"id"`
	//点赞表里会保证：同一个账号不能重复点赞同一个视频
	//两个字段的index名字都是一样，表示两个字段其一构成了一个联合唯一索引
	VideoID   uint `gorm:"uniqueIndex:idx_like_video_account;not null" json:"video_id"`
	AccountID uint `gorm:"uniqueIndex:idx_like_video_account;not null" json:"account_id"`
	//注意这里字段名是 CreatedAt，GORM 对这个名字有默认识别能力，创建记录时会自动填充时间
	CreatedAt time.Time `json:"created_at"`
}
type LikeRequest struct {
	VideoID uint `json:"video_id"`
}
