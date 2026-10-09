package main

import (
	"database/sql"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/phrynus/plog/gormlog"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var (
	_model *gorm.DB
	_sql   *sql.DB
)

func LoadModel() error {
	_model, err = gorm.Open(sqlite.Open("data.db"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
		Logger: gormlog.NewDefault(),
	})
	if err != nil {
		return err
	}
	_sql, err = _model.DB()
	if err != nil {
		return err
	}

	_sql.Exec("PRAGMA journal_mode=WAL")
	_sql.Exec("PRAGMA synchronous=NORMAL")
	_sql.Exec("PRAGMA busy_timeout=5000")
	_sql.Exec("PRAGMA cache_size=-64000")
	_sql.Exec("PRAGMA mmap_size=268435456")
	_sql.Exec("PRAGMA temp_store=MEMORY")

	// 迁移所有模型
	_sql.Exec("PRAGMA foreign_keys = ON")
	if err = _model.AutoMigrate(
		&Grid{},
	); err != nil {
		return err
	}
	_sql.Exec("PRAGMA foreign_keys = OFF")

	return nil
}

// 用户主表
type Grid struct {
	ID int64     `gorm:"primaryKey;uniqueIndex;autoIncrement:false" json:"id"`
	Ca time.Time `gorm:"autoCreateTime;" json:"ca"`
	Ua time.Time `gorm:"autoUpdateTime;" json:"ua"`

	Qty         string `gorm:"size:32;" json:"qty"`           // 货币数量
	BuyID       string `gorm:"size:32;" json:"buy_id"`        // 买入订单号
	SellID      string `gorm:"size:32;" json:"sell_id"`       // 卖出订单号
	BuyOrderID  string `gorm:"size:32;" json:"buy_order_id"`  // 买入挂单号
	SellOrderID string `gorm:"size:32;" json:"sell_order_id"` // 卖出挂单号
	BuyPrice    string `gorm:"size:32;" json:"buy_price"`     // 买入价格
	SellPrice   string `gorm:"size:32;" json:"sell_price"`    // 卖出价格
}
