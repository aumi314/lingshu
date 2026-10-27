package api

import (
	"errors"
	"net/http"
	"sort"

	"github.com/aumi314/lingshu/internal/relay"
	"github.com/aumi314/lingshu/internal/store"
)

// 上游余额查询：按需查一次，只更新渠道上的快照，不参与扣费、选路和日志。

func (s *Server) queryChannelBalance(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	t, err := s.Store.BalanceTarget(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "渠道不存在，可能已被删除，请刷新页面")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !relay.CanQueryBalance(t.Template) {
		writeError(w, http.StatusBadRequest, "这个渠道没有余额接口，目前只有 DeepSeek、Kimi、OpenRouter 支持")
		return
	}

	bal, err := s.Relay.FetchBalance(r.Context(), t.Template, t.BaseURL, t.APIKey, t.ProxyURL)
	if err != nil {
		// 失败只记错误和时间，保留上一次查到的数字
		if _, saveErr := s.Store.SaveChannelBalance(r.Context(), id, nil, err.Error()); saveErr != nil {
			writeStoreError(w, saveErr)
			return
		}
		writeError(w, http.StatusBadGateway, "查询余额失败："+err.Error())
		return
	}

	items := make([]store.BalanceItem, 0, len(bal))
	for currency, amount := range bal {
		items = append(items, store.BalanceItem{Currency: currency, Amount: amount})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Currency < items[j].Currency })
	checkedAt, err := s.Store.SaveChannelBalance(r.Context(), id, items, "")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balance": items, "checked_at": checkedAt, "error": ""})
}
