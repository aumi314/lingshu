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
