package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// 项目额度（docs/decisions.md「项目额度」）：创建人从自己余额转入项目池，成员用项目 Key 调用时从池里扣。
// 池子和个人余额一样有 balance / reserved，预扣用一条带行锁的语句，结算和个人余额同一个事务。

// weekStartSQL 是本周开始的时刻：北京时间周一 0 点（和分时段价格同一个时区）。
const weekStartSQL = `(date_trunc('week', now() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai')`

// weekSpentSQL 是项目本周已经花掉的钱（$1 换成项目 id 的表达式）。
const weekSpentSQL = `COALESCE((SELECT sum(cost) FROM request_logs WHERE project_id = %s AND created_at >= ` + weekStartSQL + `), 0)`

func weekSpent(projectExpr string) string { return strings.Replace(weekSpentSQL, "%s", projectExpr, 1) }

type Project struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	InviteCode  string           `json:"invite_code,omitempty"` // 只给创建人和管理员
	OwnerID     int64            `json:"owner_id"`
	OwnerName   string           `json:"owner_name"`
	Balance     decimal.Decimal  `json:"balance"`
	Reserved    decimal.Decimal  `json:"reserved"`
	WeeklyLimit *decimal.Decimal `json:"weekly_limit"`
	WeekSpent   decimal.Decimal  `json:"week_spent"`
	Frozen      bool             `json:"frozen"`
	Closed      bool             `json:"closed"`
	GroupIDs    []int64          `json:"group_ids"`
	Members     int              `json:"members"`   // 正式成员数（含创建人）
	MyStatus    string           `json:"my_status"` // 当前用户的身份：owner / active / pending，管理员列表里为空
	CreatedAt   time.Time        `json:"created_at"`
}

const projectColumns = `p.id, p.name, p.invite_code, p.owner_id, o.username, p.balance::text, p.reserved::text, p.weekly_limit::text,
	` + `%WEEK%` + `::text, p.frozen, p.closed_at IS NOT NULL,
	COALESCE((SELECT array_agg(pg.group_id ORDER BY pg.group_id) FROM project_groups pg WHERE pg.project_id = p.id), '{}'),
	(SELECT count(*) FROM project_members m WHERE m.project_id = p.id AND m.status = 'active'), p.created_at`

func projectSelect() string { return strings.Replace(projectColumns, "%WEEK%", weekSpent("p.id"), 1) }

func scanProject(row pgx.Row, extra ...any) (*Project, error) {
	var p Project
	var balance, reserved, spent string
	var limit *string
	dest := append([]any{&p.ID, &p.Name, &p.InviteCode, &p.OwnerID, &p.OwnerName, &balance, &reserved, &limit, &spent,
		&p.Frozen, &p.Closed, &p.GroupIDs, &p.Members, &p.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, translate(err)
	}
	p.Balance, p.Reserved, p.WeekSpent, p.WeeklyLimit = mustDecimal(balance), mustDecimal(reserved), mustDecimal(spent), optDecimal(limit)
	return &p, nil
}

// projectEntity 是操作日志里项目的快照：规则、开放分组、成员变化都看得到；邀请码只记“改过了”，不记明文。
var projectEntity = entity{"project", "name", `
	SELECT jsonb_build_object('name', p.name, 'weekly_limit', p.weekly_limit::text, 'frozen', p.frozen, 'closed', p.closed_at IS NOT NULL,
	       'invite_code', md5(p.invite_code),
	       'groups', COALESCE((SELECT jsonb_agg(g.name ORDER BY g.name) FROM project_groups pg JOIN groups g ON g.id = pg.group_id WHERE pg.project_id = p.id), '[]'),
	       'members', COALESCE((SELECT jsonb_agg(u.username ORDER BY u.username) FROM project_members m JOIN users u ON u.id = m.user_id WHERE m.project_id = p.id AND m.status = 'active'), '[]'),
	       'pending', COALESCE((SELECT jsonb_agg(u.username ORDER BY u.username) FROM project_members m JOIN users u ON u.id = m.user_id WHERE m.project_id = p.id AND m.status = 'pending'), '[]'))
	FROM projects p WHERE p.id = $1`, []string{"invite_code"}}

func newInviteCode() (string, error) {
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]), nil // 8 位大写字母和数字
}

// CreateProject 建项目，创建人自动是正式成员。是否允许创建由接口层判断。
func (s *Store) CreateProject(ctx context.Context, ownerID int64, name string) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &id, ownerID, func() error {
			code, err := newInviteCode()
			if err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `INSERT INTO projects (name, invite_code, owner_id) VALUES ($1, $2, $3) RETURNING id`, name, code, ownerID).Scan(&id); err != nil {
				return translate(err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO project_members (project_id, user_id, status) VALUES ($1, $2, 'active')`, id, ownerID)
			return err
		})
	})
	return id, err
}

// MyProjects 列出用户创建、加入或正在申请的项目（未解散的）。邀请码只给创建人。
func (s *Store) MyProjects(ctx context.Context, userID int64) ([]*Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectSelect()+`, CASE WHEN p.owner_id = $1 THEN 'owner' ELSE m.status END
		FROM project_members m JOIN projects p ON p.id = m.project_id JOIN users o ON o.id = p.owner_id
		WHERE m.user_id = $1 AND p.closed_at IS NULL ORDER BY p.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		var status string
		p, err := scanProject(rows, &status)
		if err != nil {
			return nil, err
		}
		p.MyStatus = status
		if status != "owner" {
			p.InviteCode = ""
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllProjects 给管理员：所有项目，含已解散的。
func (s *Store) AllProjects(ctx context.Context) ([]*Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectSelect()+` FROM projects p JOIN users o ON o.id = p.owner_id ORDER BY p.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProjectByID(ctx context.Context, id int64) (*Project, error) {
	return scanProject(s.Pool.QueryRow(ctx, `SELECT `+projectSelect()+` FROM projects p JOIN users o ON o.id = p.owner_id WHERE p.id = $1`, id))
}

// ProjectMemberStatus 返回用户在项目里的状态：owner / active / pending，不是成员返回空。
func (s *Store) ProjectMemberStatus(ctx context.Context, projectID, userID int64) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT CASE WHEN p.owner_id = $2 THEN 'owner' ELSE m.status END
		FROM project_members m JOIN projects p ON p.id = m.project_id WHERE m.project_id = $1 AND m.user_id = $2`, projectID, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return status, err
}

// ProjectPatch 是创建人改的规则；nil 表示不改。
type ProjectPatch struct {
	Name           *string
	SetWeeklyLimit bool
	WeeklyLimit    *decimal.Decimal // nil 表示不限
	GroupIDs       *[]int64
}

// UpdateProject 改名、每周上限、开放分组。开放的分组只能是创建人自己有权用的，防止借项目越权。
func (s *Store) UpdateProject(ctx context.Context, actorID, id int64, p ProjectPatch) error {
	if p.GroupIDs != nil && len(*p.GroupIDs) > 0 {
		var ownerID int64
		if err := s.Pool.QueryRow(ctx, `SELECT owner_id FROM projects WHERE id = $1`, id).Scan(&ownerID); err != nil {
			return translate(err)
		}
		groups, err := s.AccessibleGroups(ctx, ownerID)
		if err != nil {
			return err
		}
		allowed := map[int64]bool{}
		for _, g := range groups {
			allowed[g.ID] = true
		}
		for _, gid := range *p.GroupIDs {
			if !allowed[gid] {
				return &InputError{Msg: "只能开放你自己有权使用的服务分组"}
			}
		}
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &id, actorID, func() error {
			if err := execOne(ctx, tx, `UPDATE projects SET name = COALESCE($2, name),
				weekly_limit = CASE WHEN $3 THEN $4::numeric ELSE weekly_limit END WHERE id = $1 AND closed_at IS NULL`,
				id, p.Name, p.SetWeeklyLimit, decimalArg(p.WeeklyLimit)); err != nil {
				return err
			}
			if p.GroupIDs == nil {
				return nil
			}
			if _, err := tx.Exec(ctx, `DELETE FROM project_groups WHERE project_id = $1`, id); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO project_groups (project_id, group_id) SELECT $1, unnest($2::bigint[]) ON CONFLICT DO NOTHING`, id, *p.GroupIDs)
			return translate(err)
		})
	})
}

// ResetInviteCode 换一个新的邀请码，旧码立即失效。
func (s *Store) ResetInviteCode(ctx context.Context, actorID, id int64) (string, error) {
	code, err := newInviteCode()
	if err != nil {
		return "", err
	}
	return code, pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &id, actorID, func() error {
			return execOne(ctx, tx, `UPDATE projects SET invite_code = $2 WHERE id = $1 AND closed_at IS NULL`, id, code)
		})
	})
}

// JoinProject 按邀请码申请加入，等创建人审批。返回项目名。
func (s *Store) JoinProject(ctx context.Context, userID int64, code string) (string, error) {
	var id int64
	var name string
	err := s.Pool.QueryRow(ctx, `SELECT id, name FROM projects WHERE invite_code = $1 AND closed_at IS NULL`, strings.ToUpper(strings.TrimSpace(code))).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &InputError{Msg: "邀请码不对，或者项目已解散，请向项目负责人确认"}
	}
	if err != nil {
		return "", err
	}
	return name, pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &id, userID, func() error {
			tag, err := tx.Exec(ctx, `INSERT INTO project_members (project_id, user_id, status) VALUES ($1, $2, 'pending') ON CONFLICT DO NOTHING`, id, userID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return &InputError{Msg: "你已经是这个项目的成员，或者已经申请过了"}
			}
			return nil
		})
	})
}

type ProjectMember struct {
	UserID     int64           `json:"user_id"`
	Username   string          `json:"username"`
	Status     string          `json:"status"` // owner / active / pending
	WeekSpent  decimal.Decimal `json:"week_spent"`
	TotalSpent decimal.Decimal `json:"total_spent"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ProjectMembers 列出成员、申请人和每个人本周、累计在这个项目里花了多少。
func (s *Store) ProjectMembers(ctx context.Context, id int64) ([]ProjectMember, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT m.user_id, u.username, CASE WHEN p.owner_id = m.user_id THEN 'owner' ELSE m.status END,
		       COALESCE((SELECT sum(cost) FROM request_logs l WHERE l.project_id = p.id AND l.user_id = m.user_id AND l.created_at >= `+weekStartSQL+`), 0)::text,
		       COALESCE((SELECT sum(cost) FROM request_logs l WHERE l.project_id = p.id AND l.user_id = m.user_id), 0)::text, m.created_at
		FROM project_members m JOIN projects p ON p.id = m.project_id JOIN users u ON u.id = m.user_id
		WHERE m.project_id = $1 ORDER BY (p.owner_id = m.user_id) DESC, m.status, m.created_at`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProjectMember, error) {
		var m ProjectMember
		var week, total string
		err := r.Scan(&m.UserID, &m.Username, &m.Status, &week, &total, &m.CreatedAt)
		m.WeekSpent, m.TotalSpent = mustDecimal(week), mustDecimal(total)
		return m, err
	})
}

// ApproveMember 通过申请。
func (s *Store) ApproveMember(ctx context.Context, actorID, projectID, userID int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &projectID, actorID, func() error {
			return execOne(ctx, tx, `UPDATE project_members SET status = 'active' WHERE project_id = $1 AND user_id = $2 AND status = 'pending'`, projectID, userID)
		})
	})
}

// RemoveMember 拒绝申请、移出成员或自己退出；创建人不能被移出。移出后他的项目 Key 在调用时被拒（查不到正式成员）。
func (s *Store) RemoveMember(ctx context.Context, actorID, projectID, userID int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &projectID, actorID, func() error {
			return execOne(ctx, tx, `DELETE FROM project_members m USING projects p
				WHERE m.project_id = p.id AND m.project_id = $1 AND m.user_id = $2 AND p.owner_id <> $2`, projectID, userID)
		})
	})
}

// KeyProjectID 返回用户这把 key 所属的项目，个人 key 返回 0。
func (s *Store) KeyProjectID(ctx context.Context, userID, keyID int64) (int64, error) {
	var id *int64
	err := s.Pool.QueryRow(ctx, `SELECT project_id FROM api_keys WHERE id = $1 AND user_id = $2`, keyID, userID).Scan(&id)
	if err != nil {
		return 0, translate(err)
	}
	if id == nil {
		return 0, nil
	}
	return *id, nil
}

var ErrProjectBusy = errors.New("项目还有请求在进行，稍后再试")

// TransferProject 在创建人余额和项目池之间转钱：amount 为正是转入，为负是取回。
// 两边都只能动可用部分（余额 − 冻结），进行中请求冻结的钱不动；创建人的余额流水记一笔，标明项目。
func (s *Store) TransferProject(ctx context.Context, ownerID, projectID int64, amount decimal.Decimal) error {
	if amount.IsZero() {
		return &InputError{Msg: "金额不能为 0"}
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var name, userAvail, projectAvail string
		var closed bool
		// 先锁用户再锁项目，所有转账同一顺序，不会互相等死
		if err := tx.QueryRow(ctx, `SELECT (balance - reserved)::text FROM users WHERE id = $1 FOR UPDATE`, ownerID).Scan(&userAvail); err != nil {
			return translate(err)
		}
		err := tx.QueryRow(ctx, `SELECT name, (balance - reserved)::text, closed_at IS NOT NULL FROM projects WHERE id = $1 AND owner_id = $2 FOR UPDATE`,
			projectID, ownerID).Scan(&name, &projectAvail, &closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if closed {
			return &InputError{Msg: "项目已解散"}
		}
		if amount.IsPositive() && mustDecimal(userAvail).LessThan(amount) {
			return &InputError{Msg: "你的可用余额不够（当前 $" + mustDecimal(userAvail).StringFixed(4) + "，已扣除进行中请求冻结的额度）"}
		}
		if amount.IsNegative() && mustDecimal(projectAvail).LessThan(amount.Neg()) {
			return &InputError{Msg: "项目池可取回的不够（当前 $" + mustDecimal(projectAvail).StringFixed(4) + "，进行中请求冻结的额度不能取回）"}
		}
		note := "转入项目「" + name + "」"
		if amount.IsNegative() {
			note = "从项目「" + name + "」取回"
		}
		return moveProjectMoney(ctx, tx, ownerID, projectID, amount, note)
	})
}

// moveProjectMoney 从用户余额挪 amount 到项目池（负数反向），记余额流水和操作日志。调用方已锁好两行。
func moveProjectMoney(ctx context.Context, tx pgx.Tx, ownerID, projectID int64, amount decimal.Decimal, note string) error {
	if _, err := tx.Exec(ctx, `UPDATE projects SET balance = balance + $2::numeric WHERE id = $1`, projectID, amount.String()); err != nil {
		return err
	}
	delta := amount.Neg()
	var after string
	if err := tx.QueryRow(ctx, `UPDATE users SET balance = balance + $2::numeric WHERE id = $1 RETURNING balance::text`, ownerID, delta.String()).Scan(&after); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO balance_adjustments (user_id, amount, balance_after, operator_id, note, project_id)
		VALUES ($1, $2::numeric, $3::numeric, $1, $4, $5)`, ownerID, delta.String(), after, note, projectID); err != nil {
		return err
	}
	return recordBalance(ctx, tx, &ownerID, ownerID, delta, after, note, "", "project.transfer")
}

// CloseProject 解散：池里剩下的钱全部退回创建人，之后项目 Key 不能再用。有请求在进行时不解散。
func (s *Store) CloseProject(ctx context.Context, ownerID, projectID int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &projectID, ownerID, func() error {
			if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, ownerID); err != nil {
				return err
			}
			var name, balance, reserved string
			var closed bool
			err := tx.QueryRow(ctx, `SELECT name, balance::text, reserved::text, closed_at IS NOT NULL FROM projects WHERE id = $1 AND owner_id = $2 FOR UPDATE`,
				projectID, ownerID).Scan(&name, &balance, &reserved, &closed)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if closed {
				return &InputError{Msg: "项目已经解散了"}
			}
			if mustDecimal(reserved).IsPositive() {
				return ErrProjectBusy
			}
			if left := mustDecimal(balance); !left.IsZero() {
				if err := moveProjectMoney(ctx, tx, ownerID, projectID, left.Neg(), "项目「"+name+"」解散，余额退回"); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE projects SET closed_at = now() WHERE id = $1`, projectID)
			return err
		})
	})
}

// SetProjectFrozen 管理员冻结或解冻项目。冻结后项目 Key 调用被拒，钱和成员不动。
func (s *Store) SetProjectFrozen(ctx context.Context, actorID, projectID int64, frozen bool) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &projectID, actorID, func() error {
			return execOne(ctx, tx, `UPDATE projects SET frozen = $2 WHERE id = $1`, projectID, frozen)
		})
	})
}

// ProjectHold 是项目池预扣的结果。
type ProjectHold struct {
	Held       decimal.Decimal  // 实际冻结的金额，0 表示拒绝
	Available  decimal.Decimal  // 冻结前池子可用（余额 − 冻结）
	WeekRemain *decimal.Decimal // 冻结前本周还能花多少（扣掉进行中的冻结），nil 表示不限
}

// ReserveProject 从项目池冻结额度，同时受池子可用余额和每周上限限制，规则和个人余额的 Reserve 一样：
// 够就冻结 amount；不够但项目没有别的请求在进行、两边都还有余量时，放行这一个并冻结剩下的全部（单个请求小额透支）。
func (s *Store) ReserveProject(ctx context.Context, projectID int64, amount decimal.Decimal) (ProjectHold, error) {
	var h ProjectHold
	var avail, held string
	var remain *string
	err := s.Pool.QueryRow(ctx, `
		WITH p AS (
			SELECT balance - reserved AS avail, reserved,
			       weekly_limit - `+weekSpent("$1")+` - reserved AS remain
			FROM projects WHERE id = $1 FOR UPDATE),
		cap AS (SELECT avail, reserved, remain, LEAST(avail, COALESCE(remain, avail)) AS room FROM p)
		UPDATE projects SET reserved = projects.reserved + LEAST($2::numeric, cap.room)
		FROM cap WHERE projects.id = $1 AND (cap.room >= $2::numeric OR (cap.reserved = 0 AND cap.room > 0))
		RETURNING LEAST($2::numeric, cap.room)::text, cap.avail::text, cap.remain::text`, projectID, amount.String()).Scan(&held, &avail, &remain)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.Pool.QueryRow(ctx, `SELECT (balance - reserved)::text, (weekly_limit - `+weekSpent("$1")+` - reserved)::text FROM projects WHERE id = $1`,
			projectID).Scan(&avail, &remain)
		h.Available, h.WeekRemain = mustDecimal(avail), optDecimal(remain)
		return h, err
	}
	if err != nil {
		return h, err
	}
	h.Held, h.Available, h.WeekRemain = mustDecimal(held), mustDecimal(avail), optDecimal(remain)
	return h, nil
}
