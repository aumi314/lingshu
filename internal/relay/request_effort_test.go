package relay

import "testing"

func TestReasoningEffort(t *testing.T) {
	for body, want := range map[string]string{
		`{"model":"m","reasoning_effort":"high"}`:                                       "high",
		`{"model":"m","reasoning":{"effort":"low","summary":"auto"}}`:                   "low",
		`{"model":"m","output_config":{"effort":"max"},"thinking":{"type":"adaptive"}}`: "max",
		`{"model":"m","thinking":{"type":"enabled","budget_tokens":8000}}`:              "budget:8000",
		`{"model":"m","thinking":{"type":"adaptive"}}`:                                  "adaptive",
		`{"model":"m","thinking":{"type":"disabled"}}`:                                  "disabled",
		`{"model":"m"}`: "",
	} {
		req, err := parseRequest([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if req.effort != want {
			t.Errorf("%s: 推理强度应为 %q，实际 %q", body, want, req.effort)
		}
	}
}

func TestMaxOutputTokens(t *testing.T) {
	for body, want := range map[string]int64{
		`{"model":"m","max_tokens":1024}`:            1024, // chat、messages
		`{"model":"m","max_completion_tokens":2048}`: 2048, // chat 新字段
		`{"model":"m","max_output_tokens":4096}`:     4096, // responses
		`{"model":"m"}`:                              0,
		`{"model":"m","max_tokens":"oops"}`:          0,
	} {
		req, err := parseRequest([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if req.maxOutput != want {
			t.Errorf("%s: 最大输出应为 %d，实际 %d", body, want, req.maxOutput)
		}
	}
}
