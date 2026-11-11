package api

import "testing"

func TestReplyText(t *testing.T) {
	cases := []struct{ proto, body, want string }{
		{"chat", "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\ndata: [DONE]\n\n", "你好"},
		{"chat", `{"choices":[{"message":{"content":"整段"}}]}`, "整段"},
		{"messages", "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"想\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"答\"}}\n\n", "答"},
		{"messages", `{"type":"message","content":[{"type":"text","text":"整段"},{"type":"tool_use","name":"x"}]}`, "整段"},
		{"responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"增\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"量\"}\n\n", "增量"},
		{"responses", `{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"整段"}]}]}`, "整段"},
	}
	for _, c := range cases {
		if got := replyText(c.proto, []byte(c.body)); got != c.want {
			t.Errorf("%s %q → %q，期望 %q", c.proto, c.body[:min(30, len(c.body))], got, c.want)
		}
	}
}
