package store

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/aumi314/lingshu/internal/payments"
	"github.com/shopspring/decimal"
)

func paymentFixture(t *testing.T) (*Store, payments.Config) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,balance) VALUES(1,'payer','unused',5); INSERT INTO groups(id,name) VALUES(1,'default'); INSERT INTO api_keys(id,user_id,name,key_hash,key_prefix,group_id) VALUES(1,1,'test','test-hash','test',1)`); err != nil {
		t.Fatal(err)
	}
	c := payments.DefaultConfig("epay", decimal.RequireFromString("6.7"))
	c.Values = map[string]string{"gateway": "https://pay.example", "merchant_id": "100"}
	c.Secrets["key"] = "fixture-secret"
	c, err := s.SavePaymentConfig(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func paymentOrder(t *testing.T, s *Store, c payments.Config, key string) payments.Order {
	t.Helper()
	o, err := s.CreateTopup(context.Background(), 1, c, decimal.NewFromInt(2), decimal.RequireFromString("13.4"), "alipay", key, false)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func paid(o payments.Order) payments.Event {
	return payments.Event{OrderID: o.ID, Status: "success", Environment: o.Environment, TransactionID: "receipt-" + o.ID, Currency: o.Currency, Amount: o.Amount, Total: o.Amount, BuyerIdentity: payments.BuyerID(o)}
}

func checkPaymentState(t *testing.T, s *Store, o payments.Order, status, balance string, entries int) {
	t.Helper()
	ctx := context.Background()
	got, err := s.Topup(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	var b string
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT balance::text FROM users WHERE id=1`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM balance_adjustments WHERE topup_order_id=$1`, o.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if got.Status != status || !decimal.RequireFromString(b).Equal(decimal.RequireFromString(balance)) || n != entries {
		t.Fatalf("status=%s balance=%s ledger=%d", got.Status, b, n)
	}
}

func TestTopupConcurrentNotificationsAndModelDebit(t *testing.T) {
	s, c := paymentFixture(t)
	ctx := context.Background()
	o := paymentOrder(t, s, c, "parallel")
	retry := paymentOrder(t, s, c, "parallel")
	if retry.ID != o.ID {
		t.Fatal("request retry created another order")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 25)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, paid(o)) }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- s.SettleRequest(ctx, &RequestLog{RequestID: "parallel-debit", UserID: 1, APIKeyID: 1, GroupID: 1, Protocol: "chat", Model: "model", Status: "success", HTTPStatus: 200, Cost: decimal.RequireFromString("0.25"), GroupRatio: decimal.NewFromInt(1)})
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	checkPaymentState(t, s, o, "success", "6.75", 1)
	// 已成功订单不能被后到的失败通知覆盖。
	e := paid(o)
	e.Status = "failed"
	if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, e); err != nil {
		t.Fatal(err)
	}
	other := paymentOrder(t, s, c, "second")
	e = paid(other)
	e.TransactionID = paid(o).TransactionID
	if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, e); err == nil {
		t.Fatal("one receipt credited two orders")
	}
	checkPaymentState(t, s, other, "pending", "6.75", 0)
}

func TestTopupValidationRollbackAndLateSuccess(t *testing.T) {
	s, c := paymentFixture(t)
	ctx := context.Background()
	o := paymentOrder(t, s, c, "rollback")
	for _, change := range []func(*payments.Event){func(e *payments.Event) { e.Amount = decimal.NewFromInt(1) }, func(e *payments.Event) { e.Currency = "USD" }, func(e *payments.Event) { e.Environment = "test" }, func(e *payments.Event) { e.Tax = decimal.NewFromInt(1) }, func(e *payments.Event) { e.TransactionID = "" }} {
		e := paid(o)
		change(&e)
		if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, e); err == nil {
			t.Fatal("invalid payment accepted")
		}
		checkPaymentState(t, s, o, "pending", "5", 0)
	}
	if _, err := s.Pool.Exec(ctx, `CREATE FUNCTION reject_test_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected ledger failure'; END $$; CREATE TRIGGER reject_test_ledger BEFORE INSERT ON balance_adjustments FOR EACH ROW EXECUTE FUNCTION reject_test_ledger()`); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, paid(o)); err == nil {
		t.Fatal("expected rollback")
	}
	checkPaymentState(t, s, o, "pending", "5", 0)
	if _, err := s.Pool.Exec(ctx, `DROP TRIGGER reject_test_ledger ON balance_adjustments`); err != nil {
		t.Fatal(err)
	}
	e := paid(o)
	e.Status = "expired"
	if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, e); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, paid(o)); err != nil {
		t.Fatal(err)
	}
	checkPaymentState(t, s, o, "success", "7", 1)
	// 进程重启后仍使用持久化订单及唯一流水。
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, paid(o)); err != nil {
		t.Fatal(err)
	}
	checkPaymentState(t, s, o, "success", "7", 1)
}

func TestPaymentVersionsAndSandbox(t *testing.T) {
	s, c := paymentFixture(t)
	ctx := context.Background()
	original := c
	c.Enabled = true
	c.UnitPrice = decimal.NewFromInt(7)
	c, err := s.SavePaymentConfig(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if c.VersionID == original.VersionID || c.CallbackVersionID != original.CallbackVersionID {
		t.Fatal("price changes must snapshot without rotating webhook")
	}
	old, err := s.PaymentConfigVersion(ctx, original.VersionID)
	if err != nil || !old.UnitPrice.Equal(original.UnitPrice) {
		t.Fatal("old quote mutated", err)
	}
	c.Secrets = map[string]string{"key": "new-key"}
	c.Environment = "test"
	c, err = s.SavePaymentConfig(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if c.CallbackVersionID == original.CallbackVersionID {
		t.Fatal("credentials and environment must rotate callback")
	}
	o := paymentOrder(t, s, c, "sandbox")
	if err := s.ApplyTopupEvent(ctx, c.Provider, c.VersionID, paid(o)); err != nil {
		t.Fatal(err)
	}
	checkPaymentState(t, s, o, "success", "5", 0)
	claimed, err := s.ClaimTopupCheckout(ctx, o.ID)
	if err != nil || claimed {
		t.Fatal("completed order claimed")
	}
	o = paymentOrder(t, s, c, "session")
	claimed, err = s.ClaimTopupCheckout(ctx, o.ID)
	if err != nil || !claimed {
		t.Fatal(err)
	}
	claimed, err = s.ClaimTopupCheckout(ctx, o.ID)
	if err != nil || claimed {
		t.Fatal("duplicate external session")
	}
	if err := s.SaveTopupCheckout(ctx, o.ID, &payments.Action{Type: "redirect", URL: "https://checkout.example", CheckoutID: "session"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Topup(ctx, o.ID)
	if err != nil || reloaded.Action == nil || reloaded.CheckoutID != "session" {
		t.Fatal("checkout not persisted", err)
	}
}

func TestPaymentMigrationPreservesOldBalancesAndLedger(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `CREATE TABLE schema_migrations(version TEXT PRIMARY KEY,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	names, _ := fs.Glob(migrationFS, "migrations/*.sql")
	for _, name := range names {
		if strings.Contains(name, "0013_") {
			break
		}
		b, _ := migrationFS.ReadFile(name)
		if _, err := s.Pool.Exec(ctx, string(b)); err != nil {
			t.Fatal(err)
		}
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		if _, err := s.Pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,balance,role) VALUES(1,'old-admin','unused',12.345,'admin'); INSERT INTO balance_adjustments(user_id,amount,balance_after,operator_id,note) VALUES(1,12.345,12.345,1,'old entry')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var balance, note string
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT u.balance::text,b.note FROM users u JOIN balance_adjustments b ON b.user_id=u.id WHERE u.id=1`).Scan(&balance, &note); err != nil {
		t.Fatal(err)
	}
	if !decimal.RequireFromString(balance).Equal(decimal.RequireFromString("12.345")) || note != "old entry" {
		t.Fatal("old ledger changed")
	}
	for _, table := range []string{"payment_services", "payment_config_versions", "topup_orders"} {
		if err := s.Pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&count); err != nil || count != 0 {
			t.Fatal("payment tables not empty", err)
		}
	}
}
