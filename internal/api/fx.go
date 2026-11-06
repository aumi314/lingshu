package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/aumi314/lingshu/internal/fx"
	"github.com/aumi314/lingshu/internal/store"
)

// fxStatus 是实时汇率的拉取状态，存在 settings 的 fx_status（不进操作日志）。
type fxStatus struct {
	Source    string     `json:"source"`
	Date      string     `json:"date,omitempty"` // 汇率数据的日期
	Rate      string     `json:"rate,omitempty"` // 最近一次拉到的市场汇率
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

var fxClient = &http.Client{Timeout: 15 * time.Second}

func (s *Server) fxMode(ctx context.Context) string {
	if v, err := s.Store.Setting(ctx, "fx_mode"); err == nil && v == "live" {
		return "live"
	}
	return "manual"
}

func (s *Server) fxStatus(ctx context.Context) fxStatus {
	st := fxStatus{Source: fx.Source}
	if v, err := s.Store.Setting(ctx, "fx_status"); err == nil {
		json.Unmarshal([]byte(v), &st)
	}
	return st
}

// RefreshFX 在实时模式下拉一次市场汇率：有变化就更新系统汇率（操作日志记为系统）；
// 失败或变动超过 10% 时保留原汇率，把原因写进状态给管理员看。
func (s *Server) RefreshFX(ctx context.Context) error {
	if s.fxMode(ctx) != "live" {
		return nil
	}
	url := s.FXURL
	if url == "" {
		url = fx.URL
	}
	now := time.Now().UTC()
	st := fxStatus{Source: fx.Source, CheckedAt: &now}
	rate, date, err := fx.Fetch(ctx, fxClient, url)
	current := s.Store.CNYPerUSD(ctx)
	switch {
	case err != nil:
		st.Error = err.Error() + "，继续使用当前汇率"
	case fx.TooFar(current, rate):
		st.Rate, st.Date = rate.String(), date
		st.Error = "市场汇率 " + rate.String() + " 与当前 " + current.String() + " 相差超过 10%，未采用；确认无误请切到手动填写"
		err = errors.New(st.Error)
	default:
		st.Rate, st.Date = rate.String(), date
		if !rate.Equal(current) {
			// 系统动作：不带操作人
			err = s.Store.SetSettings(context.WithoutCancel(store.WithActor(ctx, 0)), [][2]string{{"cny_per_usd", rate.String()}})
			if err != nil {
				st.Error = "保存汇率失败：" + err.Error()
			}
		}
	}
	b, _ := json.Marshal(st)
	if serr := s.Store.SetStatusSetting(context.WithoutCancel(ctx), "fx_status", string(b)); serr != nil && err == nil {
		err = serr
	}
	return err
}

// RunFX 启动后每小时拉一次（数据每天更新一次，每小时足够及时）。
func (s *Server) RunFX(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if err := s.RefreshFX(ctx); err != nil {
			slog.Warn("实时汇率更新失败", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
