package video

import "time"

type Video struct {
	ID uint `gorm:"primaryKey" json:"id"`
	//index 表示创建索引，方便加快查询速度
	AuthorID    uint   `gorm:"index;not null" json:"author_id"`
	Username    string `gorm:"type:varchar(255);not null" json:"username"`
	Title       string `gorm:"type:varchar(255);not null" json:"title"`
	Description string `gorm:"type:varchar(255);" json:"description,omitempty"`
	PlayURL     string `gorm:"type:varchar(255);not null" json:"play_url"`
	CoverURL    string `gorm:"type:varchar(255);not null" json:"cover_url"`
	//autoCreateTime 创建记录时，GORM 自动把当前时间写进去
	//index:idx_videos_create_time,sort:desc  表示给创建时间建一个倒序索引，也就是最新发布的视频排前面
	//index:idx_videos_popularity_time_id,priority:2,sort:desc  这是热榜相关的复合索引的一部分，它和下面的 Popularity 配合使用
	CreateTime time.Time `gorm:"autoCreateTime;index:idx_videos_create_time,sort:desc;index:idx_videos_popularity_time_id,priority:2,sort:desc" json:"create_time"`
	LikesCount int64     `gorm:"column:likes_count;not null;default:0;index:idx_videos_likes_count_id,priority:1,sort:desc" json:"likes_count"`
	Popularity int64     `gorm:"column:popularity;not null;default:0;index:idx_videos_popularity_time_id,priority:1,sort:desc" json:"popularity"`
}

type PublishVideoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	PlayURL     string `json:"play_url"`
	CoverURL    string `json:"cover_url"`
}

type GetDetailRequest struct {
	ID uint `json:"id"`
}

type ListByAuthorIDRequest struct {
	AuthorID uint `json:"author_id"`
}
