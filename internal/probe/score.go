package probe

import (
	"fmt"
	"math/rand/v2"
	"sort"
)

// TVD 是两组答案的总变差距离：0 表示分布相同，1 表示完全不重叠。和脚本的 tvd 一致。
func TVD(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return -1
	}
	ca, cb := map[string]float64{}, map[string]float64{}
	for _, x := range a {
		ca[x]++
	}
	for _, x := range b {
		cb[x]++
	}
	sum := 0.0
	for k := range ca {
		d := ca[k]/float64(len(a)) - cb[k]/float64(len(b))
		if d < 0 {
			d = -d
		}
		sum += d
	}
	for k := range cb {
		if _, ok := ca[k]; !ok {
			sum += cb[k] / float64(len(b))
		}
	}
	return sum / 2
}

// Distance 是被测答案和某个官方型号在共同题目上的平均总变差；n 是参与比较的题数。
func Distance(answers map[string][]string, b *Baseline) (float64, int) {
	sum, n := 0.0, 0
	for q, a := range answers {
		ref := b.Answers[q]
		if len(a) == 0 || len(ref) == 0 {
			continue
		}
		sum += TVD(a, ref)
		n++
	}
	if n == 0 {
		return 1, 0
	}
	return sum / float64(n), n
}

// Ranked 是被测答案到各官方型号的距离，从近到远。
type Ranked struct {
	Model    string  `json:"model"`
	Distance float64 `json:"distance"`
	Prompts  int     `json:"prompts"`
}

func Rank(answers map[string][]string, models []string) []Ranked {
	_, bs, _ := Load()
	var out []Ranked
	for _, m := range models {
		b := bs[m]
		if b == nil {
			continue
		}
		d, n := Distance(answers, b)
		if n > 0 {
			out = append(out, Ranked{Model: m, Distance: round3(d), Prompts: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
	return out
}

func round3(x float64) float64 { return float64(int(x*1000+0.5)) / 1000 }

// SeparatingPrompts 返回某个家族里区分作用较强的题：同家族各官方型号之间的差距明显大于各自的波动
// （GPT 和 Claude 各算各的，Claude 样本加进来不改变 GPT 的题池）。
func SeparatingPrompts(minScore float64, family string) []string {
	s, bs, _ := Load()
	models := FamilyModels(family) // 固定顺序：型号之间的差距是一个型号的前半对另一个的后半，顺序要和脚本一致
	var out []string
	for _, q := range s.Fingerprint.Prompts {
		var halves [][2][]string
		for _, m := range models {
			a := bs[m].Answers[q]
			if len(a) >= 6 {
				halves = append(halves, [2][]string{a[:len(a)/2], a[len(a)/2:]})
			}
		}
		if len(halves) < 2 {
			continue
		}
		self, between := 0.0, 0.0
		pairs := 0
		for i, h := range halves {
			self += TVD(h[0], h[1])
			for _, g := range halves[i+1:] {
				between += TVD(h[0], g[1])
				pairs++
			}
		}
		if between/float64(pairs)-self/float64(len(halves)) >= minScore {
			out = append(out, q)
		}
	}
	return out
}

// ---- 数学题：每次检测按随机种子当场出题，标准答案由灵枢算，中转无法事先背答案 ----

// MathProblem 是一道带标准答案的题。
type MathProblem struct {
	Kind     string `json:"kind"`
	Question string `json:"question"`
	Answer   int64  `json:"answer"`
}

// NewMathProblem 按题型生成一道题（题型和脚本 make_problem 一致，参数随机）。
func NewMathProblem(kind string, rng *rand.Rand) MathProblem {
	between := func(lo, hi int) int { return lo + rng.IntN(hi-lo+1) }
	switch kind {
	case "recurrence", "recurrence_hard":
		mods, nLo, nHi, linear := []int{97, 101, 103, 107, 109}, 60, 120, false
		if kind == "recurrence_hard" {
			mods, nLo, nHi, linear = []int{997, 1009, 1013}, 150, 300, true
		}
		mod, n := mods[rng.IntN(len(mods))], between(nLo, nHi)
		a, c, x := between(2, 30), between(1, 30), between(2, 50)
		b := 0
		if linear {
			b = between(1, 30)
		}
		x1 := x
		for i := 0; i < n-1; i++ {
			x = (a*x*x + b*x + c) % mod
		}
		expr := fmt.Sprintf("%d*x(k)^2 + %d", a, c)
		if linear {
			expr = fmt.Sprintf("%d*x(k)^2 + %d*x(k) + %d", a, b, c)
		}
		return MathProblem{kind, fmt.Sprintf("Let x1 = %d and x(k+1) = (%s) mod %d. What is x%d? Answer with the number only.", x1, expr, mod, n), int64(x)}
	case "lattice", "lattice_hard":
		lo, hi, blocks := 6, 9, 3
		if kind == "lattice_hard" {
			lo, hi, blocks = 10, 12, 6
		}
		w, h := between(lo, hi), between(lo, hi)
		blocked := map[[2]int]bool{}
		for len(blocked) < blocks {
			blocked[[2]int{between(1, w-1), between(1, h-1)}] = true
		}
		ways := make([][]int64, w+1)
		for i := range ways {
			ways[i] = make([]int64, h+1)
		}
		for i := 0; i <= w; i++ {
			for j := 0; j <= h; j++ {
				switch {
				case blocked[[2]int{i, j}]:
				case i == 0 && j == 0:
					ways[i][j] = 1
				default:
					if i > 0 {
						ways[i][j] += ways[i-1][j]
					}
					if j > 0 {
						ways[i][j] += ways[i][j-1]
					}
				}
			}
		}
		var pts [][2]int
		for p := range blocked {
			pts = append(pts, p)
		}
		sort.Slice(pts, func(i, j int) bool { return pts[i][0] < pts[j][0] || pts[i][0] == pts[j][0] && pts[i][1] < pts[j][1] })
		list := ""
		for i, p := range pts {
			if i > 0 {
				list += ", "
			}
			list += fmt.Sprintf("(%d,%d)", p[0], p[1])
		}
		return MathProblem{kind, fmt.Sprintf("On a grid, count the monotone lattice paths from (0,0) to (%d,%d) using unit steps right (+1,0) or up (0,+1) that never visit any of the points %s. Answer with the number only.", w, h, list), ways[w][h]}
	case "subset_mod":
		n, k, m := between(18, 24), between(4, 5), between(7, 13)
		var count int64
		var walk func(start, left, sum int)
		walk = func(start, left, sum int) {
			if left == 0 {
				if sum%m == 0 {
					count++
				}
				return
			}
			for v := start; v <= n; v++ {
				walk(v+1, left-1, sum+v)
			}
		}
		walk(1, k, 0)
		return MathProblem{kind, fmt.Sprintf("How many %d-element subsets of {1, 2, ..., %d} have a sum divisible by %d? Answer with the number only.", k, n, m), count}
	default: // digit_sum
		t := between(20, 40)
		dp := make([]int64, t+1)
		dp[0] = 1
		for range 7 {
			next := make([]int64, t+1)
			for s := 0; s <= t; s++ {
				for d := 0; d <= 9 && d <= s; d++ {
					next[s] += dp[s-d]
				}
			}
			dp = next
		}
		return MathProblem{"digit_sum", fmt.Sprintf("How many integers n with 1 <= n < 10000000 have a digit sum equal to %d? Answer with the number only.", t), dp[t]}
	}
}
