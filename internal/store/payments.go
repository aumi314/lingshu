package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"

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
		rate := decimal.RequireFromString("6.7")
		if v, e := s.Setting(ctx, "cny_per_usd"); e == nil {
			if d, e := decimal.NewFromString(v); e == nil {
				rate = d
			}
		}
		return payments.DefaultConfig(provider, rate), nil
	}
	if err != nil {
		return payments.Config{}, err
	}
	c, err := decodePaymentConfig(data, secrets)
	c.VersionID = id
	c.Enabled = enabled
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
		_, err := tx.Exec(ctx, `INSERT INTO payment_services(provider,enabled,version_id) VALUES($1,$2,$3) ON CONFLICT(provider) DO UPDATE SET enabled=EXCLUDED.enabled,version_id=EXCLUDED.version_id`, c.Provider, c.Enabled, c.VersionID)
		return err
	})
	return c, err
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
	return scanTopup(s.Pool.QueryRow(ctx, `INSERT INTO topup_orders(id,user_id,provider,version_id,environment,method,credit_usd,amount,currency,unit_price,request_key,admin_test) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(user_id,request_key) DO UPDATE SET request_key=EXCLUDED.request_key RETURNING `+topupColumns, id, userID, c.Provider, c.VersionID, c.Environment, method, credit.String(), amount.String(), c.Currency, c.UnitPrice.String(), key, adminTest))
}

func (s *Store) Topup(ctx context.Context, id string) (payments.Order, error) {
	return scanTopup(s.Pool.QueryRow(ctx, `SELECT `+topupColumns+` FROM topup_orders WHERE id=$1`, id))
}

func (s *Store) Topups(ctx context.Context, userID int64, page, size int) ([]payments.Order, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM topup_orders WHERE ($1::bigint=0 OR user_id=$1)`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+topupColumns+` FROM topup_orders WHERE ($1::bigint=0 OR user_id=$1) ORDER BY created_at DESC,id LIMIT $2 OFFSET $3`, userID, size, (page-1)*size)
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
		_, err = tx.Exec(ctx, `INSERT INTO balance_adjustments(user_id,amount,balance_after,operator_id,note,topup_order_id) VALUES($1,$2,$3,NULL,$4,$5)`, o.UserID, o.CreditUSD.String(), after, "在线充值 · "+provider, o.ID)
		return err
	})
}
