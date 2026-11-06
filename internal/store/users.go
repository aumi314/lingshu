package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrUsernameTaken = errors.New("用户名已存在")

// CreateUser 创建用户，passwordHash 由调用方用 bcrypt 生成。
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, userEntity, &id, -1, func() error {
			if err := tx.QueryRow(ctx,
				`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
				username, passwordHash, role,
			).Scan(&id); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `INSERT INTO user_group_members (user_id, user_group_id)
				SELECT $1, id FROM user_groups WHERE is_default`, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("默认用户分组不存在")
			}
			return nil
		})
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, ErrUsernameTaken
		}
		return 0, fmt.Errorf("创建用户失败: %w", err)
	}
	return id, nil
}

// CountEnabledSuperAdmins 返回启用中的超级管理员数量，用于“至少保留一个”的保护。
func (s *Store) CountEnabledSuperAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'super_admin' AND enabled`).Scan(&n)
	return n, err
}
