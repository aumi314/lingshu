package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/relay/usage"
)

// bridgeFixture 造两个渠道：只填了 Messages 地址的（Responses 入口要靠网关转接），
// 和填了 Responses 地址的（原生）。两个渠道都挂了同一个模型。
func bridgeFixture(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO users(id,username,password_hash) VALUES(1,'u','x');
		INSERT INTO groups(id,name) VALUES(1,'g');
		INSERT INTO api_keys(id,user_id,name,key_hash,key_prefix,group_id) VALUES(1,1,'k','h','k',1);
		INSERT INTO models(id,name,input_price,output_price) VALUES(1,'m1',1,2),(2,'m2',1,2);
		INSERT INTO channels(id,name,template,api_key,priority,enabled,base_url_chat,base_url_messages,base_url_responses)
		VALUES(1,'messages-only','','k1',10,true,'','https://up.example/anthropic',''),
		       (2,'responses-native','','k2',5,true,'','https://up.example/anthropic2','https://up.example/v1');
		INSERT INTO channel_keys(channel_id,api_key) VALUES(1,'k1'),(2,'k2');
		INSERT INTO channel_groups(channel_id,group_id) VALUES(1,1),(2,1);
		INSERT INTO channel_models(channel_id,model_id) VALUES(1,1),(2,1),(1,2);`); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFindRoutesResponsesFallsBackToMessagesUpstream(t *testing.T) {
	s := bridgeFixture(t)
	ctx := context.Background()

	routes, err := s.FindRoutes(ctx, 1, "responses", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("Responses 入口应同时命中原生和转接渠道，实际 %d 条", len(routes))
	}
	byChannel := map[int64]Route{}
	for _, r := range routes {
		byChannel[r.ChannelID] = r
	}
	bridged, ok := byChannel[1]
	if !ok {
		t.Fatal("缺少只有 Messages 地址的渠道")
	}
	if bridged.UpstreamProtocol != usage.Messages {
		t.Errorf("转接渠道上游协议应为 messages，实际 %q", bridged.UpstreamProtocol)
	}
	if bridged.BaseURL != "https://up.example/anthropic" {
		t.Errorf("转接渠道应走 Messages 地址，实际 %q", bridged.BaseURL)
	}
	native, ok := byChannel[2]
	if !ok {
		t.Fatal("缺少原生 Responses 渠道")
	}
	if native.UpstreamProtocol != usage.Responses {
		t.Errorf("原生渠道上游协议应为 responses，实际 %q", native.UpstreamProtocol)
	}
	if native.BaseURL != "https://up.example/v1" {
		t.Errorf("原生渠道应走 Responses 地址，实际 %q", native.BaseURL)
	}
	if routes[0].ChannelID != 1 {
		t.Errorf("优先级高的渠道应排前面，实际顺序 %d %d", routes[0].ChannelID, routes[1].ChannelID)
	}

	// 其它协议保持原生行为：上游协议跟入口一致，地址用对应协议那一列
	messages, err := s.FindRoutes(ctx, 1, "messages", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("Messages 入口应命中两个渠道，实际 %d 条", len(messages))
	}
	for _, r := range messages {
		if r.UpstreamProtocol != usage.Messages {
			t.Errorf("Messages 入口上游协议应为 messages，实际 %q", r.UpstreamProtocol)
		}
	}
	chat, err := s.FindRoutes(ctx, 1, "chat", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(chat) != 0 {
		t.Errorf("没有 Chat 地址就不该命中，实际 %d 条", len(chat))
	}
}

func TestModelProtocolsCountsBridgedResponses(t *testing.T) {
	s := bridgeFixture(t)
	ctx := context.Background()

	protocols, err := s.ModelProtocols(ctx, 1, "m1")
	if err != nil {
		t.Fatal(err)
	}
	has := func(p string) bool {
		for _, x := range protocols {
			if x == p {
				return true
			}
		}
		return false
	}
	if !has("responses") {
		t.Errorf("有 Messages 渠道时 Responses 也该可用，实际 %v", protocols)
	}
	if !has("messages") {
		t.Errorf("缺少 messages，实际 %v", protocols)
	}
	if has("chat") {
		t.Errorf("不该出现 chat，实际 %v", protocols)
	}
}

func TestPricedModelsReportsNativeAndBridgedProtocols(t *testing.T) {
	s := bridgeFixture(t)
	ctx := context.Background()

	priced, err := s.PricedModels(ctx, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]PricedModel{}
	for _, p := range priced {
		byName[p.Name] = p
	}
	m1, ok := byName["m1"]
	if !ok {
		t.Fatal("缺少模型 m1")
	}
	if !contains(m1.Protocols, "responses") {
		t.Errorf("客户端可用入口应含 responses，实际 %v", m1.Protocols)
	}
	if !contains(m1.NativeProtocols, "responses") {
		t.Errorf("m1 有原生 Responses 渠道，原生协议应含 responses，实际 %v", m1.NativeProtocols)
	}
	if !contains(m1.NativeProtocols, "messages") {
		t.Errorf("原生协议应含 messages，实际 %v", m1.NativeProtocols)
	}
	if len(m1.BridgedProtocols) != 0 {
		t.Errorf("m1 有原生 Responses，不需要转接，实际 %v", m1.BridgedProtocols)
	}
	// 只有 Messages 地址的模型：Responses 靠转接
	m2, ok := byName["m2"]
	if !ok {
		t.Fatal("缺少模型 m2")
	}
	if !contains(m2.Protocols, "responses") {
		t.Errorf("m2 客户端可用入口应含 responses，实际 %v", m2.Protocols)
	}
	if contains(m2.NativeProtocols, "responses") {
		t.Errorf("m2 原生协议不该含 responses，实际 %v", m2.NativeProtocols)
	}
	if len(m2.BridgedProtocols) != 1 || m2.BridgedProtocols[0] != "responses" {
		t.Errorf("m2 转接协议应为 responses，实际 %v", m2.BridgedProtocols)
	}
}

func TestSettleRequestRecordsUpstreamProtocolOperationAndDiagnostics(t *testing.T) {
	s := bridgeFixture(t)
	ctx := context.Background()

	if err := s.SettleRequest(ctx, &RequestLog{
		RequestID: "r-bridge", UserID: 1, APIKeyID: 1, GroupID: 1,
		Protocol: "responses", UpstreamProtocol: usage.Messages,
		Operation: "compaction", ConversionDiagnostics: json.RawMessage(`[{"field":"input[0].content","note":"dropped"}]`),
		Model: "m1", Status: "success", HTTPStatus: 200,
		Cost: decimal.RequireFromString("0.1"), GroupRatio: decimal.NewFromInt(1),
	}); err != nil {
		t.Fatal(err)
	}
	var upstream, operation string
	var diagnostics []byte
	if err := s.Pool.QueryRow(ctx, `SELECT upstream_protocol, operation, conversion_diagnostics FROM request_logs WHERE request_id='r-bridge'`).
		Scan(&upstream, &operation, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if upstream != "messages" || operation != "compaction" {
		t.Fatalf("上游协议/用途记错: %q %q", upstream, operation)
	}
	var decoded []map[string]string
	if err := json.Unmarshal(diagnostics, &decoded); err != nil {
		t.Fatalf("诊断不是合法 JSON: %v (%s)", err, diagnostics)
	}
	if len(decoded) != 1 || decoded[0]["field"] != "input[0].content" || decoded[0]["note"] != "dropped" {
		t.Errorf("诊断内容不对: %s", diagnostics)
	}

	// 没带的字段按默认值落库：上游协议跟入口，用途正常生成，诊断空数组
	if err := s.SettleRequest(ctx, &RequestLog{
		RequestID: "r-plain", UserID: 1, APIKeyID: 1, GroupID: 1,
		Protocol: "chat", Model: "m1", Status: "success", HTTPStatus: 200,
		Cost: decimal.RequireFromString("0.1"), GroupRatio: decimal.NewFromInt(1),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT upstream_protocol, operation, conversion_diagnostics FROM request_logs WHERE request_id='r-plain'`).
		Scan(&upstream, &operation, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if upstream != "chat" || operation != "generation" || string(diagnostics) != "[]" {
		t.Fatalf("默认值不对: %q %q %s", upstream, operation, diagnostics)
	}

	logs, _, err := s.ListLogs(ctx, LogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]LogEntry{}
	for _, l := range logs {
		byID[l.RequestID] = l
	}
	bridge := byID["r-bridge"]
	if bridge.UpstreamProtocol != "messages" || bridge.Operation != "compaction" {
		t.Fatalf("日志详情没带出新字段: %q %q", bridge.UpstreamProtocol, bridge.Operation)
	}
	if len(bridge.ConversionDiagnostics) == 0 || string(bridge.ConversionDiagnostics) == "null" {
		t.Errorf("日志详情应带出诊断: %s", bridge.ConversionDiagnostics)
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// 迁移 0034 是给已有数据的库补列：老日志的上游协议按当时的入口协议回填。
func TestMigration0034BackfillsLegacyLogs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `
		CREATE TABLE request_logs (id BIGSERIAL PRIMARY KEY, protocol TEXT NOT NULL);
		INSERT INTO request_logs (protocol) VALUES ('messages'), ('chat');`); err != nil {
		t.Fatal(err)
	}
	body, err := migrationFS.ReadFile("migrations/0034_log_upstream_protocol.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	rows, err := s.Pool.Query(ctx, `SELECT protocol, upstream_protocol, operation, conversion_diagnostics FROM request_logs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var protocol, upstream, operation string
		var diagnostics []byte
		if err := rows.Scan(&protocol, &upstream, &operation, &diagnostics); err != nil {
			t.Fatal(err)
		}
		if upstream != protocol {
			t.Errorf("老日志上游协议应按入口协议回填: %q → %q", protocol, upstream)
		}
		if operation != "generation" || string(diagnostics) != "[]" {
			t.Errorf("默认用途/诊断不对: %q %s", operation, diagnostics)
		}
	}
}
