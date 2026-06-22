package message

import "time"

type Message struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	FromID  uint   `gorm:"index:idx_message_from;not null" json:"from_id"`
	ToID    uint   `gorm:"index:idx_message_to;not null" json:"to_id"`
	Content string `gorm:"type:text;not null" json:"content"`
	//是否已读
	IsRead    bool      `gorm:"default:false" json:"is_read"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
