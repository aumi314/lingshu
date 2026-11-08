package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"time"
)

var ErrEmailTaken = errors.New("邮箱已注册")
var ErrEmailCode = errors.New("验证码无效、已过期或错误次数已达上限")
var ErrEmailCooldown = errors.New("请等待 60 秒后再发送验证码")

type MailAccount struct {
	Version      int64      `json:"-"`
	ID           int64      `json:"id"`
	Email        string     `json:"email"`
	ClientID     string     `json:"client_id"`
	RefreshToken string     `json:"-"`
	Enabled      bool       `json:"enabled"`
	SortOrder    int        `json:"sort_order"`
	Configured   bool       `json:"refresh_token_configured"`
	CheckedAt    *time.Time `json:"checked_at"`
	LastStatus   string     `json:"last_status"`
	LastError    string     `json:"last_error"`
}

func (s *Store) MailAccounts(ctx context.Context) ([]MailAccount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,email,client_id,refresh_token,enabled,sort_order,checked_at,last_status,last_error,config_version FROM mail_accounts ORDER BY sort_order,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MailAccount{}
	for rows.Next() {
		var a MailAccount
		if err := rows.Scan(&a.ID, &a.Email, &a.ClientID, &a.RefreshToken, &a.Enabled, &a.SortOrder, &a.CheckedAt, &a.LastStatus, &a.LastError, &a.Version); err != nil {
			return nil, err
		}
		a.Configured = a.RefreshToken != ""
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) MailReady(ctx context.Context) (bool, error) {
	var ready bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_accounts WHERE enabled AND checked_at IS NOT NULL AND refresh_token<>'')`).Scan(&ready)
	return ready, err
}
func mailAudit(ctx context.Context, tx pgx.Tx, a MailAccount, action string) error {
	// 明确构造无密钥快照，授权更新只记录发生过变更。
	raw, _ := json.Marshal(map[string]any{"email": a.Email, "client_id": a.ClientID, "enabled": a.Enabled, "sort_order": a.SortOrder})
	return record(ctx, tx, operation{action: action, targetType: "mail_account", targetID: fmt.Sprint(a.ID), targetName: a.Email, changes: []Change{{Field: "account", To: raw}}})
}
func (s *Store) SaveMailAccount(ctx context.Context, a MailAccount) (int64, error) {
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if a.ID == 0 {
			if err := tx.QueryRow(ctx, `INSERT INTO mail_accounts(email,client_id,refresh_token,enabled,sort_order) VALUES($1,$2,$3,$4,$5) RETURNING id`, a.Email, a.ClientID, a.RefreshToken, a.Enabled, a.SortOrder).Scan(&a.ID); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `UPDATE mail_accounts SET config_version=config_version+1,email=$2,client_id=$3,refresh_token=CASE WHEN $4='' THEN refresh_token ELSE $4 END,enabled=$5,sort_order=$6,checked_at=CASE WHEN email<>$2 OR client_id<>$3 OR $4<>'' THEN NULL ELSE checked_at END WHERE id=$1`, a.ID, a.Email, a.ClientID, a.RefreshToken, a.Enabled, a.SortOrder)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		return mailAudit(ctx, tx, a, "mail_account.save")
	})
	return a.ID, translate(err)
}
func (s *Store) DeleteMailAccount(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var a MailAccount
		a.ID = id
		if err := tx.QueryRow(ctx, `DELETE FROM mail_accounts WHERE id=$1 RETURNING email`, id).Scan(&a.Email); err != nil {
			return translate(err)
		}
		return mailAudit(ctx, tx, a, "mail_account.delete")
	})
}
func (s *Store) OrderMailAccounts(ctx context.Context, ids []int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM mail_accounts`).Scan(&count); err != nil {
			return err
		}
		if len(ids) != count {
			return errors.New("账号顺序需要包含全部账号")
		}
		seen := map[int64]bool{}
		for i, id := range ids {
			if seen[id] {
				return errors.New("账号顺序包含重复项")
			}
			seen[id] = true
			tag, err := tx.Exec(ctx, `UPDATE mail_accounts SET sort_order=$2 WHERE id=$1`, id, i)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrNotFound
			}
		}
		raw, _ := json.Marshal(ids)
		return record(ctx, tx, operation{action: "mail_account.order", targetType: "mail_account", changes: []Change{{Field: "ids", To: raw}}})
	})
}
func (s *Store) MailResult(ctx context.Context, id int64, status, msg string, checked bool, version ...int64) error {
	var expected int64
	if len(version) > 0 {
		expected = version[0]
	}
	_, err := s.Pool.Exec(ctx, `UPDATE mail_accounts SET last_status=$2,last_error=$3,checked_at=CASE WHEN $4 THEN now() WHEN $2='authorization_error' THEN NULL ELSE checked_at END WHERE id=$1 AND ($5::bigint=0 OR config_version=$5)`, id, status, msg, checked, expected)
	return err
}
func (s *Store) RotateMailToken(ctx context.Context, id int64, clientID, old, next string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE mail_accounts SET refresh_token=$3 WHERE id=$1 AND refresh_token=$2 AND client_id=$4`, id, old, next, clientID)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("发信账号授权已更新，请重试")
	}
	return err
}
func EmailCodeHash(email, code string) string {
	h := sha256.Sum256([]byte(email + ":" + code))
	return fmt.Sprintf("%x", h)
}
func (s *Store) ClaimEmailCode(ctx context.Context, email, hash string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		flags, err := registrationFlags(ctx, tx)
		if err != nil {
			return err
		}
		if !flags.Enabled || !flags.EmailRequired {
			return ErrRegistrationClosed
		}

		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=$1)`, email).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrEmailTaken
		}
		tag, err := tx.Exec(ctx, `INSERT INTO registration_emails(email,pending_hash,pending_at) VALUES($1,$2,now()) ON CONFLICT(email) DO UPDATE SET pending_hash=$2,pending_at=now(),last_sent_at=now() WHERE registration_emails.last_sent_at<=now()-interval '60 seconds' AND (registration_emails.pending_at IS NULL OR registration_emails.pending_at<now()-interval '2 minutes')`, email, hash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrEmailCooldown
		}
		return nil
	})
}
func (s *Store) FinishEmailCode(ctx context.Context, email, hash, status string, keep bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE registration_emails SET code_hash=CASE WHEN $4 THEN $2 ELSE code_hash END,expires_at=CASE WHEN $4 THEN now()+interval '15 minutes' ELSE expires_at END,attempts=CASE WHEN $4 THEN 0 ELSE attempts END,pending_hash='',pending_at=NULL,send_status=$3 WHERE email=$1 AND pending_hash=$2`, email, hash, status, keep)
	return err
}
func (s *Store) RegisterUserWithEmail(ctx context.Context, username, passwordHash, email, code, sessionHash string, expires time.Time) error {
	return s.RegisterUser(ctx, username, passwordHash, "", email, code, sessionHash, expires)
}

// RegisterUser 是旧调用入口，等价于没有同意协议；配置了协议后注册会被拒绝。
func (s *Store) RegisterUser(ctx context.Context, username, passwordHash, registrationCode, email, emailCode, sessionHash string, expires time.Time) error {
	return s.RegisterUserWithConsent(ctx, username, passwordHash, registrationCode, email, emailCode, sessionHash, false, "", expires)
}

// RegisterUserWithConsent 注册用户。legalAccepted 是用户是否勾选同意协议，legalVersion 是用户提交的协议版本号，
// 两者和当前配置比对一致才算同意；同意记录写进用户行，只有自助注册才有。
func (s *Store) RegisterUserWithConsent(ctx context.Context, username, passwordHash, registrationCode, email, emailCode, sessionHash string, legalAccepted bool, legalVersion string, expires time.Time) error {
	var validation error
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		flags, err := registrationFlags(ctx, tx)
		if err != nil {
			return err
		}
		if !flags.Enabled {
			return ErrRegistrationClosed
		}
		docs, err := legalDocuments(ctx, tx)
		if err != nil {
			return err
		}
		if err := checkLegalConsent(docs, legalAccepted, legalVersion); err != nil {
			return err
		}
		var codeID int64
		if flags.CodeRequired {
			if err := tx.QueryRow(ctx, `SELECT id FROM registration_codes WHERE code_hash=$1 AND used_at IS NULL AND revoked_at IS NULL FOR UPDATE`, registrationCodeHash(registrationCode)).Scan(&codeID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrInvalidRegistrationCode
				}
				return err
			}
		}
		if flags.EmailRequired {
			var hash, pending string
			var exp time.Time
			var attempts int
			if err := tx.QueryRow(ctx, `SELECT code_hash,expires_at,attempts,pending_hash FROM registration_emails WHERE email=$1 FOR UPDATE`, email).Scan(&hash, &exp, &attempts, &pending); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrEmailCode
				}
				return err
			}
			if pending != "" || hash == "" || time.Now().After(exp) || attempts >= 5 {
				return ErrEmailCode
			}
			if hash != EmailCodeHash(email, emailCode) {
				_, err := tx.Exec(ctx, `UPDATE registration_emails SET attempts=attempts+1 WHERE email=$1`, email)
				validation = ErrEmailCode
				return err
			}
		}
		var userEmail any
		if flags.EmailRequired {
			userEmail = email
		}
		var consentVersion any
		if docs.Required() {
			consentVersion = docs.Version
		}
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,email,legal_version,legal_accepted_at)
			VALUES($1,$2,'user',$3,$4::text,CASE WHEN $4::text IS NULL THEN NULL ELSE now() END) RETURNING id`, username, passwordHash, userEmail, consentVersion).Scan(&id); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				if strings.Contains(pe.ConstraintName, "email") {
					return ErrEmailTaken
				}
				return ErrUsernameTaken
			}
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO user_group_members(user_id,user_group_id) SELECT $1,id FROM user_groups WHERE is_default`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("默认用户分组不存在")
		}
		if flags.CodeRequired {
			if _, err = tx.Exec(ctx, `UPDATE registration_codes SET used_by=$2,used_at=now() WHERE id=$1`, codeID, id); err != nil {
				return err
			}
		}
		if flags.EmailRequired {
			if _, err = tx.Exec(ctx, `DELETE FROM registration_emails WHERE email=$1`, email); err != nil {
				return err
			}
		}
		after, err := takeSnapshot(ctx, tx, userEntity, id)
		if err != nil {
			return err
		}
		if err = recordSnapshots(WithActor(ctx, id), tx, userEntity, id, id, nil, after); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, sessionHash, id, expires)
		return err
	})
	if err != nil {
		return translate(err)
	}
	return validation
}
