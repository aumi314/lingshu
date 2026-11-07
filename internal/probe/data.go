// Package probe 是模型检测：按检测题向一个渠道的模型发请求，和官方样本对比，判断有没有掺水
// （换模型、降推理强度、注入内容、协议不规范）。口径见 docs/decisions.md「模型检测」。
//
// 检测题和官方样本在 data/ 下，编译进程序；官方样本由本机脚本 scripts/probe/probe.py 用官方账号生成，两边共用同一份数据和同一套算法。
package probe

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

//go:embed data/suite.json data/baselines/*.json
var dataFS embed.FS

// Suite 是检测题（data/suite.json）。只追加新题、不改已有题目的文字：改了就是另一道题，旧样本不能比。
type Suite struct {
	Version int `json:"version"`
	Tokens  struct {
		Suffix string            `json:"suffix"`
		Probes map[string]string `json:"probes"`
	} `json:"tokens"`
	Fingerprint struct {
		Effort   string    `json:"effort"`
		Samples  int       `json:"samples"`
		Suffix   string    `json:"suffix"`
		SuffixZh string    `json:"suffix_zh"`
		Prompts  []string  `json:"prompts"`
		PairSets []PairSet `json:"pair_sets"`
	} `json:"fingerprint"`
	Math struct {
		Effort   string `json:"effort"`
		Problems []struct {
			Kind string `json:"kind"`
			Tier string `json:"tier"`
		} `json:"problems"`
	} `json:"math"`
	Needle struct {
		Effort string `json:"effort"`
		Tokens int    `json:"tokens"`
	} `json:"needle"`
}

// PairSet 是专门区分两个相近型号的题组（如 sol 和 astra：同一个底子，整体指纹相近）。
type PairSet struct {
	Models  []string `json:"models"`
	Samples int      `json:"samples"`
	Prompts []string `json:"prompts"`
}

// Baseline 是一个官方型号的样本（多轮合并）。
type Baseline struct {
	Model       string              // 去掉 codex/ 前缀的型号名，如 gpt-6.1-sol
	Answers     map[string][]string // 单词指纹：题目 → 答案（已归一）
	TokenBase   int                 // 分词指纹的基准输入 token（官方 Codex 环境，含 Codex 注入的提示词）
	TokenDeltas map[string]int      // 分词指纹：探针文本带来的输入 token 差值
	Math        map[string]MathBaseline
}

// MathBaseline 是某类数学题在官方样本里的表现。
type MathBaseline struct {
	Runs            int
	Correct         int
	MedianReasoning int
}

var (
	loadOnce  sync.Once
	suite     Suite
	baselines map[string]*Baseline
	loadErr   error
)

// Load 读取编译进程序的检测题和官方样本。
func Load() (*Suite, map[string]*Baseline, error) {
	loadOnce.Do(func() { loadErr = load() })
	return &suite, baselines, loadErr
}

// Family 是型号所属的家族。指纹只在同一家族的官方样本之间比（GPT 和 Claude 的题池、距离各算各的）。
func Family(model string) string {
	if strings.HasPrefix(model, "claude-") {
		return "claude"
	}
	return "gpt"
}

// FamilyModels 返回某个家族有官方样本的型号，按名字排序。
func FamilyModels(family string) []string {
	var out []string
	for _, m := range BaselineModels() {
		if Family(m) == family {
			out = append(out, m)
		}
	}
	return out
}

// BaselineModels 返回有官方样本的型号，按名字排序。
func BaselineModels() []string {
	_, bs, err := Load()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(bs))
	for m := range bs {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

type baselineFile struct {
	Model       string `json:"model"`
	Fingerprint *struct {
		Answers map[string][]string `json:"answers"`
	} `json:"fingerprint"`
	Tokens *struct {
		Base   []int          `json:"base"`
		Deltas map[string]int `json:"deltas"`
	} `json:"tokens"`
	Math *struct {
		Problems []struct {
			Kind string `json:"kind"`
		} `json:"problems"`
		Runs []struct {
			Problem         int  `json:"problem"`
			Correct         bool `json:"correct"`
			ReasoningTokens *int `json:"reasoning_tokens"`
		} `json:"runs"`
	} `json:"math"`
}

func load() error {
	raw, err := dataFS.ReadFile("data/suite.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		return fmt.Errorf("检测题格式不对: %w", err)
	}
	v1 := suite.Fingerprint.Prompts
	if len(v1) > 24 {
		v1 = v1[:24] // 第 1 版样本按序号记录答案，题目就是题库的前 24 题
	}
	baselines = map[string]*Baseline{}
	reasoning := map[string]map[string][]int{}
	files, _ := fs.Glob(dataFS, "data/baselines/*.json")
	for _, f := range files {
		if strings.HasPrefix(path.Base(f), "fingerprint-") {
			continue // 答案分布汇总，给人看的，这里直接用原始样本
		}
		raw, err := dataFS.ReadFile(f)
		if err != nil {
			return err
		}
		var bf baselineFile
		if err := json.Unmarshal(raw, &bf); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		model := strings.TrimPrefix(bf.Model, "codex/")
		b := baselines[model]
		if b == nil {
			b = &Baseline{Model: model, Answers: map[string][]string{}, TokenDeltas: map[string]int{}, Math: map[string]MathBaseline{}}
			baselines[model] = b
			reasoning[model] = map[string][]int{}
		}
		if bf.Fingerprint != nil {
			for q, ans := range bf.Fingerprint.Answers {
				if n, err := strconv.Atoi(q); err == nil && n < len(v1) {
					q = v1[n]
				}
				for _, a := range ans {
					b.Answers[q] = append(b.Answers[q], Fold(a))
				}
			}
		}
		if bf.Tokens != nil && len(bf.Tokens.Base) > 0 {
			b.TokenBase = bf.Tokens.Base[0]
			for k, v := range bf.Tokens.Deltas {
				b.TokenDeltas[k] = v
			}
		}
		if bf.Math != nil {
			for _, r := range bf.Math.Runs {
				if r.Problem >= len(bf.Math.Problems) {
					continue
				}
				kind := bf.Math.Problems[r.Problem].Kind
				mb := b.Math[kind]
				mb.Runs++
				if r.Correct {
					mb.Correct++
				}
				b.Math[kind] = mb
				if r.ReasoningTokens != nil {
					reasoning[model][kind] = append(reasoning[model][kind], *r.ReasoningTokens)
				}
			}
		}
	}
	for model, kinds := range reasoning {
		for kind, xs := range kinds {
			mb := baselines[model].Math[kind]
			mb.MedianReasoning = median(xs)
			baselines[model].Math[kind] = mb
		}
	}
	if len(baselines) == 0 {
		return fmt.Errorf("没有官方样本")
	}
	return nil
}

func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	return s[len(s)/2]
}

// Normalize 和脚本的 normalize 一致：转小写、去掉空白和标点，保留各种文字；清洗后为空（比如只回了表情）就保留原样；最多 40 个字符。
func Normalize(text string) string {
	lower := strings.ToLower(text)
	var b strings.Builder
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		out = strings.TrimSpace(text)
	}
	if utf8.RuneCountInString(out) > 40 {
		out = string([]rune(out)[:40])
	}
	return out
}

var latinExtra = map[rune]string{'ł': "l", 'ø': "o", 'đ': "d", 'ß': "ss", 'æ': "ae", 'œ': "oe", 'þ': "th", 'ð': "d"}

// Fold 用于比对：去掉拉丁字母上的重音（týr→tyr、comté→comte、ł→l），假名和汉字不动。和脚本的 fold 一致。
func Fold(answer string) string {
	var b strings.Builder
	for _, r := range answer {
		if s, ok := latinExtra[r]; ok {
			b.WriteString(s)
			continue
		}
		if r < 0x250 {
			for _, c := range norm.NFKD.String(string(r)) {
				if !unicode.Is(unicode.Mn, c) {
					b.WriteRune(c)
				}
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// HasCJK 判断题目是不是中文题（用中文后缀）。
func HasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}
