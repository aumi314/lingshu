package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

// BalanceItem 是厂商余额接口返回的一项：币种和金额，原样保存，不做汇率换算。
type BalanceItem struct {
	Currency string          `json:"currency"`
	Amount   decimal.Decimal `json:"amount"`
}

// BalanceTarget 是查询余额需要的渠道信息。
type BalanceTarget struct {
	ChannelID int64
	Template  string
	BaseURL   string
	APIKey    string
	ProxyURL  string
}

// BalanceTarget 读取渠道的模板、地址和 key；渠道不存在时返回 ErrNotFound。
func (s *Store) BalanceTarget(ctx context.Context, channelID int64) (*BalanceTarget, error) {
	var t BalanceTarget
	err := s.Pool.QueryRow(ctx, `
		SELECT id, template, base_url_chat, api_key, proxy_url FROM channels WHERE id = $1`, channelID).
		Scan(&t.ChannelID, &t.Template, &t.BaseURL, &t.APIKey, &t.ProxyURL)
	if err != nil {
		return nil, translate(err)
	}
	return &t, nil
}

// SaveChannelBalance 记下查询结果和查询时间；items 为 nil 表示查询失败，只更新错误，
// 保留上一次查到的数字。返回数据库里的查询时间，直接回给前端。
func (s *Store) SaveChannelBalance(ctx context.Context, channelID int64, items []BalanceItem, errMsg string) (time.Time, error) {
	var checkedAt time.Time
	var err error
	if items == nil {
		err = s.Pool.QueryRow(ctx, `
			UPDATE channels SET balance_checked_at = now(), balance_error = $2
			WHERE id = $1 RETURNING balance_checked_at`, channelID, errMsg).Scan(&checkedAt)
		return checkedAt, translate(err)
	}
	detail, err := json.Marshal(items)
	if err != nil {
		return time.Time{}, err
	}
	err = s.Pool.QueryRow(ctx, `
		UPDATE channels SET balance_detail = $2::jsonb, balance_checked_at = now(), balance_error = $3
		WHERE id = $1 RETURNING balance_checked_at`, channelID, string(detail), errMsg).Scan(&checkedAt)
	return checkedAt, translate(err)
}
