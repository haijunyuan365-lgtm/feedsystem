package db

import (
	"feedsystem/internal/account"
	"feedsystem/internal/config"
	"feedsystem/internal/message"
	"feedsystem/internal/social"
	video2 "feedsystem/internal/video"
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func NewDB(dbCfg config.DatabaseConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		dbCfg.Username,
		dbCfg.Password,
		dbCfg.Host,
		dbCfg.Port,
		dbCfg.DBName)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	////最多保留 10 个空闲连接。连接不用立刻关闭，下次请求可以复用。
	//sqlDB.SetMaxIdleConns(10)
	////最多同时打开 100 个连接。避免并发太高时把 MySQL 打爆
	//sqlDB.SetMaxOpenConns(100)
	////一个连接最多用 1 小时，到期后重新建连接。避免长连接一直不释放
	//sqlDB.SetConnMaxLifetime(time.Hour)

	//主动测试数据库是否真的连通。
	if err = sqlDB.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// AutoMigrate 一次性自动创建/更新今天需要的核心业务表。
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&account.Account{},
		&video2.Video{},
		&video2.Like{},
		&video2.Comment{},
		&video2.Tag{},
		&video2.VideoTag{},
		&video2.OutboxMsg{},
		&social.Social{},
		&message.Message{},
	)
}

// GORM 的 *gorm.DB 本身没有直接 Close() 方法，要先拿到底层的：sqlDB, err := gormDB.DB()
func CloseDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
