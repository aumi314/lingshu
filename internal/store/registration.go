package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrRegistrationClosed      = errors.New("自助注册未开放")
	ErrInvalidRegistrationCode = errors.New("注册码无效、已使用或已撤销")
	ErrRegistrationCodeUsed    = errors.New("注册码已使用，不能撤销")
)

type NewRegistrationCode struct {
	ID     int64  `json:"id"`
	Prefix string `json:"prefix"`
	Code   string `json:"code"`
}

type RegistrationCode struct {
	ID        int64      `json:"id"`
	Prefix    string     `json:"prefix"`
	CreatedAt time.Time  `json:"created_at"`
	UsedBy    string     `json:"used_by"`
	UsedAt    *time.Time `json:"used_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

func registrationCodeHash(code string) string {
	canonical := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	hash := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%x", hash)
}

func randomRegistrationCode() (string, string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	compact := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	prefix := compact[:10]
	var parts []string
	for len(compact) > 5 {
		parts = append(parts, compact[:5])
		compact = compact[5:]
	}
	parts = append(parts, compact)
	return strings.Join(parts, "-"), strings.ToUpper(prefix), nil
}

func (s *Store) CreateRegistrationCodes(ctx context.Context, adminID int64, count int) ([]NewRegistrationCode, error) {
	codes := make([]NewRegistrationCode, count)
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		for i := range codes {
			code, prefix, err := randomRegistrationCode()
			if err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO registration_codes (code_hash, code_prefix, created_by)
				VALUES ($1, $2, $3) RETURNING id`, registrationCodeHash(code), prefix, adminID,
			).Scan(&codes[i].ID); err != nil {
				return err
			}
			codes[i].Prefix, codes[i].Code = prefix, code
		}
		prefixes := make([]string, len(codes))
		for i, c := range codes {
			prefixes[i] = c.Prefix
		}
		to, _ := json.Marshal(prefixes)
		return record(ctx, tx, operation{action: "registration_code.create", targetType: "registration_code",
			changes: []Change{{Field: "prefixes", To: to}}})
	})
	if err != nil {
		return nil, translate(err)
	}
	return codes, nil
}

func (s *Store) ListRegistrationCodes(ctx context.Context) ([]RegistrationCode, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT rc.id, rc.code_prefix, rc.created_at, COALESCE(u.username, ''), rc.used_at, rc.revoked_at
		FROM registration_codes rc
		LEFT JOIN users u ON u.id = rc.used_by
		ORDER BY rc.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RegistrationCode
	for rows.Next() {
		var code RegistrationCode
		if err := rows.Scan(&code.ID, &code.Prefix, &code.CreatedAt, &code.UsedBy, &code.UsedAt, &code.RevokedAt); err != nil {
			return nil, err
		}
		result = append(result, code)
	}
	return result, rows.Err()
}

func (s *Store) RevokeRegistrationCode(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, codeEntity, &id, 0, func() error {
			tag, err := tx.Exec(ctx, `UPDATE registration_codes SET revoked_at = now()
				WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL`, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				var used bool
				if err := tx.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM registration_codes WHERE id = $1`, id).Scan(&used); err != nil {
					return translate(err)
				}
				if used {
					return ErrRegistrationCodeUsed
				}
			}
			return nil
		})
	})
}

// RegisterUserWithCode 保留旧调用入口，统一由注册事务校验当前限制。
func (s *Store) RegisterUserWithCode(ctx context.Context, username, passwordHash, code, sessionHash string, expires time.Time) error {
	return s.RegisterUser(ctx, username, passwordHash, code, "", "", sessionHash, expires)
}
