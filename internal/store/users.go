package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrUsernameTaken = errors.New("用户名已存在")

// CreateUser 创建用户，passwordHash 由调用方用 bcrypt 生成。
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, passwordHash, role,
	).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, ErrUsernameTaken
		}
		return 0, fmt.Errorf("创建用户失败: %w", err)
	}
	return id, nil
}
