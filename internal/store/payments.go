package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/aumi314/lingshu/internal/payments"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func (s *Store) PaymentConfig(ctx context.Context, provider string) (payments.Config, error) {
	var data, secrets []byte
	var id int64
	var enabled bool
	err := s.Pool.QueryRow(ctx, `SELECT v.id,v.config,v.secrets,p.enabled FROM payment_services p JOIN payment_config_versions v ON v.id=p.version_id WHERE p.provider=$1`, provider).Scan(&id, &data, &secrets, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return payments.DefaultConfig(provider, s.CNYPerUSD(ctx)), nil
	}
	if err != nil {
		return payments.Config{}, err
	}
	c, err := decodePaymentConfig(data, secrets)
	c.VersionID = id
	c.Enabled = enabled
	// 收人民币的服务按系统汇率报价，和用户看到的人民币金额一致（2026-11-06 决策）
	if c.Currency == "CNY" {
		c.UnitPrice = s.CNYPerUSD(ctx)
	}
	return c, err
}

func decodePaymentConfig(data, secrets []byte) (payments.Config, error) {
	var c payments.Config
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if err := json.Unmarshal(secrets, &c.Secrets); err != nil {
		return c, err
	}
	c.ConfiguredSecrets = map[string]bool{}
	for k, v := range c.Secrets {
		c.ConfiguredSecrets[k] = v != ""
	}
	return c, nil
}

func (s *Store) PaymentConfigVersion(ctx context.Context, id int64) (payments.Config, error) {
	var data, secrets []byte
	err := s.Pool.QueryRow(ctx, `SELECT config,secrets FROM payment_config_versions WHERE id=$1`, id).Scan(&data, &secrets)
	if err != nil {
		return payments.Config{}, translate(err)
	}
	c, err := decodePaymentConfig(data, secrets)
	c.VersionID = id
	return c, err
}

func (s *Store) SavePaymentConfig(ctx context.Context, c payments.Config) (payments.Config, error) {
	old, err := s.PaymentConfig(ctx, c.Provider)
	if err != nil {
		return c, err
	}
	c.CallbackVersionID = 0
	if old.VersionID > 0 && old.Environment == c.Environment && reflect.DeepEqual(old.Values, c.Values) && reflect.DeepEqual(old.Secrets, c.Secrets) {
		c.CallbackVersionID = old.CallbackVersionID
		if c.CallbackVersionID == 0 {
			c.CallbackVersionID = old.VersionID
		}
	}
	data, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	secrets, err := json.Marshal(c.Secrets)
	if err != nil {
		return c, err
	}
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO payment_config_versions(provider,config,secrets) VALUES($1,$2,$3) RETURNING id`, c.Provider, data, secrets).Scan(&c.VersionID); err != nil {
			return err
		}
		if c.CallbackVersionID == 0 {
			c.CallbackVersionID = c.VersionID
			data, err = json.Marshal(c)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_config_versions SET config=$2 WHERE id=$1`, c.VersionID, data); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_services(provider,enabled,version_id) VALUES($1,$2,$3) ON CONFLICT(provider) DO UPDATE SET enabled=EXCLUDED.enabled,version_id=EXCLUDED.version_id`, c.Provider, c.Enabled, c.VersionID); err != nil {
			return err
		}
		var before snapshot
		if old.VersionID > 0 {
			before = paymentSnapshot(old)
		}
		after := paymentSnapshot(c)
		var secrets []string
		for k := range before {
			if strings.HasPrefix(k, "secrets.") {
				secrets = append(secrets, k)
			}
		}
		for k := range after {
			if strings.HasPrefix(k, "secrets.") {
				secrets = append(secrets, k)
			}
		}
		action := "payment.update"
		if before == nil {
			action = "payment.create"
		}
		changes := diff(before, after, secrets)
		if len(changes) == 0 {
			return nil
		}
		return record(ctx, tx, operation{action: action, targetType: "payment", targetID: strconv.FormatInt(c.VersionID, 10), targetName: c.Provider, changes: changes})
	})
	return c, err
}

// paymentSnapshot 把支付配置展开成可比对的字段；密钥只保留指纹，日志里只显示“已更换”。
func paymentSnapshot(c payments.Config) snapshot {
	s := snapshot{}
	put := func(k string, v any) { s[k], _ = json.Marshal(v) }
	put("enabled", c.Enabled)
	put("environment", c.Environment)
	put("currency", c.Currency)
	put("unit_price", c.UnitPrice.String())
	put("min_usd", c.MinUSD.String())
	put("max_usd", c.MaxUSD.String())
	put("methods", c.Methods)
	for k, v := range c.Values {
		put("values."+k, v)
	}
	for k, v := range c.Secrets {
		sum := sha256.Sum256([]byte(v))
		put("secrets."+k, hex.EncodeToString(sum[:]))
	}
	return s
}

const topupColumns = `id,user_id,provider,version_id,environment,method,credit_usd::text,amount::text,currency,unit_price::text,status,checkout_id,transaction_id,tax::text,total::text,admin_test,created_at,completed_at,checkout_started,checkout_data`

func scanTopup(row pgx.Row) (payments.Order, error) {
	var o payments.Order
	var credit, amount, price, tax, total string
	var action []byte
	err := row.Scan(&o.ID, &o.UserID, &o.Provider, &o.VersionID, &o.Environment, &o.Method, &credit, &amount, &o.Currency, &price, &o.Status, &o.CheckoutID, &o.TransactionID, &tax, &total, &o.AdminTest, &o.CreatedAt, &o.CompletedAt, &o.CheckoutStarted, &action)
	if err != nil {
		return o, translate(err)
	}
	o.CreditUSD = mustDecimal(credit)
	o.Amount = mustDecimal(amount)
	o.UnitPrice = mustDecimal(price)
	o.Tax = mustDecimal(tax)
	o.Total = mustDecimal(total)
	if len(action) > 0 {
		if err := json.Unmarshal(action, &o.Action); err != nil {
			return o, err
		}
	}
	return o, nil
}

func (s *Store) CreateTopup(ctx context.Context, userID int64, c payments.Config, credit, amount decimal.Decimal, method, key string, adminTest bool) (payments.Order, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return payments.Order{}, err
	}
	id := hex.EncodeToString(b)
	var o payments.Order
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// 同一个 request_key 重复提交返回原订单；只有真正新建时才记操作日志（并发重复提交时后到的等前一个提交后什么都不做）
		var inserted string
		err := tx.QueryRow(ctx, `INSERT INTO topup_orders(id,user_id,provider,version_id,environment,method,credit_usd,amount,currency,unit_price,request_key,admin_test) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(user_id,request_key) DO NOTHING RETURNING id`, id, userID, c.Provider, c.VersionID, c.Environment, method, credit.String(), amount.String(), c.Currency, c.UnitPrice.String(), key, adminTest).Scan(&inserted)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if o, err = scanTopup(tx.QueryRow(ctx, `SELECT `+topupColumns+` FROM topup_orders WHERE user_id=$1 AND request_key=$2`, userID, key)); err != nil {
			return err
		}
		if inserted == "" {
			return nil
		}
		return recordTopupCreate(ctx, tx, o)
	})
	return o, err
}

func (s *Store) Topup(ctx context.Context, id string) (payments.Order, error) {
	return scanTopup(s.Pool.QueryRow(ctx, `SELECT `+topupColumns+` FROM topup_orders WHERE id=$1`, id))
}

// TopupFilter 筛选充值订单：用户（0 表示全部）、状态（空表示全部）、创建时间范围。
type TopupFilter struct {
	UserID   int64
	Status   string
	From, To *time.Time
}

func (s *Store) Topups(ctx context.Context, f TopupFilter, page, size int) ([]payments.Order, int, error) {
	const where = `WHERE ($1::bigint=0 OR user_id=$1) AND ($2::text='' OR status=$2)
		AND ($3::timestamptz IS NULL OR created_at >= $3) AND ($4::timestamptz IS NULL OR created_at < $4)`
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM topup_orders `+where, f.UserID, f.Status, f.From, f.To).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+topupColumns+` FROM topup_orders `+where+` ORDER BY created_at DESC,id LIMIT $5 OFFSET $6`, f.UserID, f.Status, f.From, f.To, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []payments.Order{}
	for rows.Next() {
		o, err := scanTopup(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

// ClaimTopupCheckout 只允许一个请求创建外部支付会话。超时后保留订单等待查单或通知。
func (s *Store) ClaimTopupCheckout(ctx context.Context, id string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE topup_orders SET checkout_started=true WHERE id=$1 AND status='pending' AND NOT checkout_started`, id)
	return tag.RowsAffected() == 1, err
}

func (s *Store) SaveTopupCheckout(ctx context.Context, id string, a *payments.Action) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return execOne(ctx, s.Pool, `UPDATE topup_orders SET checkout_data=$2,checkout_id=$3 WHERE id=$1`, id, data, a.CheckoutID)
}

// ApplyTopupEvent 与模型扣费共同使用 users.balance 的原子增减，订单和流水一起提交。
func (s *Store) ApplyTopupEvent(ctx context.Context, provider string, versionID int64, e payments.Event) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		o, err := scanTopup(tx.QueryRow(ctx, `SELECT `+topupColumns+` FROM topup_orders WHERE id=$1 FOR UPDATE`, e.OrderID))
		if err != nil {
			return err
		}
		if o.Provider != provider || o.VersionID != versionID {
			return payments.ErrInvalidEvent
		}
		if err := payments.ValidateEvent(o, e); err != nil {
			return err
		}
		if o.Status == "success" {
			if e.Status == "success" && o.TransactionID != e.TransactionID {
				return payments.ErrInvalidEvent
			}
			return nil
		}
		if e.Status != "success" {
			if e.Status == "failed" || e.Status == "expired" {
				_, err = tx.Exec(ctx, `UPDATE topup_orders SET status=$2 WHERE id=$1`, o.ID, e.Status)
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE topup_orders SET status='success',transaction_id=$2,tax=$3,total=$4,completed_at=now() WHERE id=$1`, o.ID, e.TransactionID, e.Tax.String(), e.Total.String()); err != nil {
			return err
		}
		if e.Environment != "prod" {
			return nil
		} // 记录沙箱成功，但不增加真实余额。
		var after string
		if err := tx.QueryRow(ctx, `UPDATE users SET balance=balance+$2::numeric WHERE id=$1 RETURNING balance::text`, o.UserID, o.CreditUSD.String()).Scan(&after); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO balance_adjustments(user_id,amount,balance_after,operator_id,note,topup_order_id) VALUES($1,$2,$3,NULL,$4,$5)`, o.UserID, o.CreditUSD.String(), after, "在线充值 · "+provider, o.ID); err != nil {
			return err
		}
		// 入账一律记为系统：用户点“核对付款”触发时 context 里是用户本人
		var system int64
		return recordBalance(ctx, tx, &system, o.UserID, o.CreditUSD, after, "在线充值 · "+provider, o.ID, "topup.credit")
	})
}
