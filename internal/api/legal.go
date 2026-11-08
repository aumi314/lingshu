package api

import (
	"net/http"
	"unicode/utf8"
)

// maxLegalRunes 是协议文本的字数上限（一份协议最多 10 万字）。
const maxLegalRunes = 100000

// legalDocs 是公开接口，未登录也能读到协议全文和当前版本号。
func (s *Server) legalDocs(w http.ResponseWriter, r *http.Request) {
	docs, err := s.Store.LegalDocuments(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

// checkLegalLength 校验协议文本长度，超限直接拒绝。
func checkLegalLength(text string) bool {
	return utf8.RuneCountInString(text) <= maxLegalRunes
}
