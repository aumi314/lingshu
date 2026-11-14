package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/billing"
)

// 项目额度（docs/decisions.md「项目额度」）：创建人从自己余额转入项目池，成员用项目 Key 调用时从池里扣。
// 池子和个人余额一样有 balance / reserved，预扣用一条带行锁的语句，结算和个人余额同一个事务。

// periodStartSQL 是当前周期开始的时刻：按业务时区（主设置里的时区，默认北京时间）的当天 0 点、本周一 0 点或本月 1 号 0 点，
// periodExpr 是成员 limit_period 列的表达式（day / week / month，正好是 date_trunc 的单位）。
// 时区名已由 billing.ParseZone 校验（只有字母、数字和 / _ + -），可以直接拼进语句。
func periodStartSQL(periodExpr string) string {
	z := billing.ZoneName()
	return "(date_trunc(" + periodExpr + ", now() AT TIME ZONE '" + z + "') AT TIME ZONE '" + z + "')"
}

// memberSpent 是成员本周期在项目里已经花掉的钱（周期按他自己的 limit_period）。
func memberSpent(projectExpr, userExpr, periodExpr string) string {
	return "COALESCE((SELECT sum(cost) FROM request_logs WHERE project_id = " + projectExpr + " AND user_id = " + userExpr + " AND created_at >= " + periodStartSQL(periodExpr) + "), 0)"
}

// LimitPeriods 是创建人给成员设额度时能选的周期。
var LimitPeriods = map[string]bool{"day": true, "week": true, "month": true}

type Project struct {
	ID         int64            `json:"id"`
	Name       string           `json:"name"`
	InviteCode string           `json:"invite_code,omitempty"` // 只给创建人和管理员
	OwnerID    int64            `json:"owner_id"`
	OwnerName  string           `json:"owner_name"`
	Balance    decimal.Decimal  `json:"balance"`
	Reserved   decimal.Decimal  `json:"reserved"`
	TotalSpent decimal.Decimal  `json:"total_spent"` // 整个项目累计花了多少
	MyLimit    *decimal.Decimal `json:"my_limit"`    // 当前用户的额度上限（创建人给每个成员单独设），nil 不限；管理员列表里为空
	MyPeriod   string           `json:"my_period"`   // day / week / month，按业务时区重置
	MySpent    decimal.Decimal  `json:"my_spent"`    // 当前用户本周期已花
	Frozen     bool             `json:"frozen"`
	Closed     bool             `json:"closed"`
	GroupIDs   []int64          `json:"group_ids"`
	Groups     json.RawMessage  `json:"groups"`    // 开放分组的 id、名称、倍率，成员建 key 时选
	Members    int              `json:"members"`   // 正式成员数（含创建人）
	MyStatus   string           `json:"my_status"` // 当前用户的身份：owner / active / pending，管理员列表里为空
	CreatedAt  time.Time        `json:"created_at"`
}

const projectColumns = `p.id, p.name, p.invite_code, p.owner_id, o.username, p.balance::text, p.reserved::text,
	COALESCE((SELECT sum(cost) FROM request_logs WHERE project_id = p.id), 0)::text, p.frozen, p.closed_at IS NOT NULL,
	COALESCE((SELECT array_agg(pg.group_id ORDER BY pg.group_id) FROM project_groups pg WHERE pg.project_id = p.id), '{}'),
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id', g.id, 'name', g.name, 'ratio', g.ratio::text, 'is_collection', g.is_collection) ORDER BY g.sort_order, g.id)
	          FROM project_groups pg JOIN groups g ON g.id = pg.group_id WHERE pg.project_id = p.id), '[]'),
	(SELECT count(*) FROM project_members m WHERE m.project_id = p.id AND m.status = 'active'), p.created_at`

func scanProject(row pgx.Row, extra ...any) (*Project, error) {
	var p Project
	var balance, reserved, spent string
	dest := append([]any{&p.ID, &p.Name, &p.InviteCode, &p.OwnerID, &p.OwnerName, &balance, &reserved, &spent,
		&p.Frozen, &p.Closed, &p.GroupIDs, &p.Groups, &p.Members, &p.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, translate(err)
	}
	p.Balance, p.Reserved, p.TotalSpent = mustDecimal(balance), mustDecimal(reserved), mustDecimal(spent)
	return &p, nil
}

// projectEntity 是操作日志里项目的快照：规则、开放分组、成员和每人额度的变化都看得到；邀请码只记“改过了”，不记明文。
var projectEntity = entity{"project", "name", `
	SELECT jsonb_build_object('name', p.name, 'frozen', p.frozen, 'closed', p.closed_at IS NOT NULL,
	       'invite_code', md5(p.invite_code),
	       'groups', COALESCE((SELECT jsonb_agg(g.name ORDER BY g.name) FROM project_groups pg JOIN groups g ON g.id = pg.group_id WHERE pg.project_id = p.id), '[]'),
	       'members', COALESCE((SELECT jsonb_agg(u.username ORDER BY u.username) FROM project_members m JOIN users u ON u.id = m.user_id WHERE m.project_id = p.id AND m.status = 'active'), '[]'),
	       'member_limits', COALESCE((SELECT jsonb_object_agg(u.username, m.period_limit::text || '/' || m.limit_period) FROM project_members m JOIN users u ON u.id = m.user_id WHERE m.project_id = p.id AND m.period_limit IS NOT NULL), '{}'),
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
	rows, err := s.Pool.Query(ctx, `SELECT `+projectColumns+`, CASE WHEN p.owner_id = $1 THEN 'owner' ELSE m.status END,
		       m.period_limit::text, m.limit_period, `+memberSpent("p.id", "m.user_id", "m.limit_period")+`::text
		FROM project_members m JOIN projects p ON p.id = m.project_id JOIN users o ON o.id = p.owner_id
		WHERE m.user_id = $1 AND p.closed_at IS NULL ORDER BY p.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		var status, period, spent string
		var limit *string
		p, err := scanProject(rows, &status, &limit, &period, &spent)
		if err != nil {
			return nil, err
		}
		p.MyStatus, p.MyPeriod, p.MyLimit, p.MySpent = status, period, optDecimal(limit), mustDecimal(spent)
		if status != "owner" {
			p.InviteCode = ""
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllProjects 给管理员：所有项目，含已解散的。
func (s *Store) AllProjects(ctx context.Context) ([]*Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectColumns+` FROM projects p JOIN users o ON o.id = p.owner_id ORDER BY p.id DESC`)
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
	return scanProject(s.Pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects p JOIN users o ON o.id = p.owner_id WHERE p.id = $1`, id))
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
	Name     *string
	GroupIDs *[]int64
}

// UpdateProject 改名、开放分组。开放的分组只能是创建人自己有权用的，防止借项目越权。
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
			if err := execOne(ctx, tx, `UPDATE projects SET name = COALESCE($2, name) WHERE id = $1 AND closed_at IS NULL`, id, p.Name); err != nil {
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
	UserID      int64            `json:"user_id"`
	Username    string           `json:"username"`
	Status      string           `json:"status"`       // owner / active / pending
	PeriodLimit *decimal.Decimal `json:"period_limit"` // 创建人给他设的额度上限，nil 不限
	LimitPeriod string           `json:"limit_period"` // day / week / month
	PeriodSpent decimal.Decimal  `json:"period_spent"` // 本周期（按他自己的周期）在这个项目里花了多少
	TotalSpent  decimal.Decimal  `json:"total_spent"`
	CreatedAt   time.Time        `json:"created_at"`
}

// ProjectMembers 列出成员、申请人，每个人的额度上限、本周期和累计在这个项目里花了多少。
func (s *Store) ProjectMembers(ctx context.Context, id int64) ([]ProjectMember, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT m.user_id, u.username, CASE WHEN p.owner_id = m.user_id THEN 'owner' ELSE m.status END,
		       m.period_limit::text, m.limit_period, `+memberSpent("p.id", "m.user_id", "m.limit_period")+`::text,
		       COALESCE((SELECT sum(cost) FROM request_logs l WHERE l.project_id = p.id AND l.user_id = m.user_id), 0)::text, m.created_at
		FROM project_members m JOIN projects p ON p.id = m.project_id JOIN users u ON u.id = m.user_id
		WHERE m.project_id = $1 ORDER BY (p.owner_id = m.user_id) DESC, m.status, m.created_at`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProjectMember, error) {
		var m ProjectMember
		var week, total string
		var limit *string
		err := r.Scan(&m.UserID, &m.Username, &m.Status, &limit, &m.LimitPeriod, &week, &total, &m.CreatedAt)
		m.PeriodLimit, m.PeriodSpent, m.TotalSpent = optDecimal(limit), mustDecimal(week), mustDecimal(total)
		return m, err
	})
}

// SetMemberLimit 创建人给正式成员（含自己）设额度上限和周期，limit 为 nil 表示不限。
func (s *Store) SetMemberLimit(ctx context.Context, actorID, projectID, userID int64, limit *decimal.Decimal, period string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return track(ctx, tx, projectEntity, &projectID, actorID, func() error {
			return execOne(ctx, tx, `UPDATE project_members SET period_limit = $3::numeric, limit_period = $4
				WHERE project_id = $1 AND user_id = $2 AND status = 'active'`, projectID, userID, decimalArg(limit), period)
		})
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

// ProjectHold 是项目预扣的结果。
type ProjectHold struct {
	Held         decimal.Decimal  // 实际冻结的金额，0 表示拒绝
	Available    decimal.Decimal  // 冻结前池子可用（余额 − 冻结）
	PeriodRemain *decimal.Decimal // 冻结前这个成员本周期还能花多少（扣掉他进行中的冻结），nil 表示不限
	Period       string           // 成员的额度周期，提示语用
}

// ReserveProject 给成员的这次请求冻结额度，同时受项目池可用余额和他自己的额度上限限制，规则和个人余额的 Reserve 一样：
// 够就冻结 amount；不够但项目没有别的请求在进行、两边都还有余量时，放行这一个并冻结剩下的全部（单个请求小额透支）。
// 池子和成员行在同一个事务里加锁（先项目后成员，和结算同一顺序），并发请求冲不破池子和个人上限。
func (s *Store) ReserveProject(ctx context.Context, projectID, userID int64, amount decimal.Decimal) (ProjectHold, error) {
	var h ProjectHold
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var avail, poolReserved string
		if err := tx.QueryRow(ctx, `SELECT (balance - reserved)::text, reserved::text FROM projects WHERE id = $1 FOR UPDATE`, projectID).Scan(&avail, &poolReserved); err != nil {
			return translate(err)
		}
		var remain *string
		if err := tx.QueryRow(ctx, `SELECT (period_limit - `+memberSpent("$1", "$2", "limit_period")+` - reserved)::text, limit_period
			FROM project_members WHERE project_id = $1 AND user_id = $2 FOR UPDATE`, projectID, userID).Scan(&remain, &h.Period); err != nil {
			return translate(err)
		}
		h.Available, h.PeriodRemain = mustDecimal(avail), optDecimal(remain)
		room := h.Available
		if h.PeriodRemain != nil {
			room = decimal.Min(room, *h.PeriodRemain)
		}
		if room.LessThan(amount) && !(mustDecimal(poolReserved).IsZero() && room.IsPositive()) {
			return nil
		}
		h.Held = decimal.Min(amount, room)
		if _, err := tx.Exec(ctx, `UPDATE projects SET reserved = reserved + $2::numeric WHERE id = $1`, projectID, h.Held.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE project_members SET reserved = reserved + $3::numeric WHERE project_id = $1 AND user_id = $2`, projectID, userID, h.Held.String())
		return err
	})
	return h, err
}
