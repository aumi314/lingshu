package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// 协议文本存在 settings 表里，和公告一样由管理员在运营设置里改。
const (
	LegalUserAgreementKey = "user_agreement"
	LegalPrivacyPolicyKey = "privacy_policy"
)

var (
	ErrLegalNotAccepted     = errors.New("请先阅读并同意用户协议和隐私政策")
	ErrLegalVersionMismatch = errors.New("协议版本已更新，请重新阅读后同意")
)

// LegalDocuments 是对外公开的协议内容，Version 由两份文本算出来。
type LegalDocuments struct {
	UserAgreement string `json:"user_agreement"`
	PrivacyPolicy string `json:"privacy_policy"`
	Version       string `json:"version"`
}

// Required 表示两份文本里有一份被配置了，此时注册必须同意。
// 空白按没配置算，和前端的判断保持一致。
func (d LegalDocuments) Required() bool {
	return strings.TrimSpace(d.UserAgreement) != "" || strings.TrimSpace(d.PrivacyPolicy) != ""
}

// LegalVersion 由两份协议文本的内容算出，同一份文本永远同一个版本号。
// 长度前缀避免两份文本拼接出歧义。
func LegalVersion(userAgreement, privacyPolicy string) string {
	h := sha256.New()
	fmt.Fprintf(h, "user_agreement:%d:", len(userAgreement))
	h.Write([]byte(userAgreement))
	fmt.Fprintf(h, "privacy_policy:%d:", len(privacyPolicy))
	h.Write([]byte(privacyPolicy))
	return hex.EncodeToString(h.Sum(nil))
}

type legalQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// legalDocuments 读两份协议文本并算出当前版本号。
func legalDocuments(ctx context.Context, q legalQuery) (LegalDocuments, error) {
	var agreement, policy *string
	err := q.QueryRow(ctx, `SELECT
		(SELECT value FROM settings WHERE key=$1),
		(SELECT value FROM settings WHERE key=$2)`,
		LegalUserAgreementKey, LegalPrivacyPolicyKey).Scan(&agreement, &policy)
	docs := LegalDocuments{}
	if agreement != nil {
		docs.UserAgreement = *agreement
	}
	if policy != nil {
		docs.PrivacyPolicy = *policy
	}
	docs.Version = LegalVersion(docs.UserAgreement, docs.PrivacyPolicy)
	return docs, err
}

// LegalDocuments 读当前生效的协议文本和版本号。
func (s *Store) LegalDocuments(ctx context.Context) (LegalDocuments, error) {
	return legalDocuments(ctx, s.Pool)
}

// checkLegalConsent 在注册事务里校验用户是否同意了当前版本的协议。
// 未配置任何协议时不做要求，配置后必须勾选同意且版本号一致。
func checkLegalConsent(docs LegalDocuments, accepted bool, version string) error {
	if !docs.Required() {
		return nil
	}
	if !accepted {
		return ErrLegalNotAccepted
	}
	if strings.TrimSpace(version) != docs.Version {
		return ErrLegalVersionMismatch
	}
	return nil
}
