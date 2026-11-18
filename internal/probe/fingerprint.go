package probe

import (
	"fmt"
	"strings"
)

func comparableFingerprint(call Call) bool {
	if call.Error != "" || !call.Done || clean(call.Text) == "" {
		return false
	}
	switch call.FinishReason {
	case "length", "max_tokens", "incomplete", "content_filter":
		return false
	}
	return call.EffortSent == "" || call.EffortSent == call.Effort
}

func clearFingerprintLeader(ranking []Ranked) (Ranked, int, bool) {
	if len(ranking) < 2 {
		return Ranked{}, 0, false
	}
	lead := int(ranking[1].Distance*1000+0.5) - int(ranking[0].Distance*1000+0.5)
	return ranking[0], lead, lead >= 50 && ranking[0].Distance <= 0.45
}

func (p *plan) fingerprintEvidence(words, pairCalls []Call) (Item, *Item) {
	item := Item{Key: "fingerprint", Title: "单词指纹", Status: Warn}
	models := FamilyModels(Family(p.spec.Baseline))
	var valid []Call
	var sessionCalls [Sessions][]Call
	var planned [Sessions]int
	for _, call := range words {
		planned[call.Session]++
		if comparableFingerprint(call) {
			valid = append(valid, call)
			sessionCalls[call.Session] = append(sessionCalls[call.Session], call)
		}
	}
	ranking := Rank(answersOf(valid), models)
	coverage := len(words) > 0 && len(valid)*100 >= len(words)*80
	var sessions []sessionRank
	var distinct = map[string]bool{}
	var labels []string
	for index, calls := range sessionCalls {
		if planned[index] == 0 || len(calls)*100 < planned[index]*70 {
			coverage = false
		}
		local := Rank(answersOf(calls), models)
		leader, _, clear := clearFingerprintLeader(local)
		session := sessionRank{Session: index + 1, Ranking: local}
		if clear && leader.Prompts >= 15 {
			session.Nearest = leader.Model
			label := leader.Model
			if label == "gpt-6.1-sol" || label == "gpt-6-astra" {
				label = "sol / astra"
			}
			distinct[label] = true
			labels = append(labels, fmt.Sprintf("第 %d 组像 %s", index+1, label))
		}
		sessions = append(sessions, session)
	}
	item.Details = map[string]any{"ranking": ranking, "sessions": sessions, "valid": len(valid), "planned": len(words)}
	var pair *Item
	if p.pair != nil {
		var pairValid []Call
		for _, call := range pairCalls {
			if comparableFingerprint(call) {
				pairValid = append(pairValid, call)
			}
		}
		pairRanking := Rank(answersOf(pairValid), p.pair.Models)
		pair = &Item{Key: "pair", Title: strings.Join(p.pair.Models, " / ") + " 区分", Status: Warn, Summary: "专用题组样本不足", Details: map[string]any{"ranking": pairRanking, "valid": len(pairValid), "planned": len(pairCalls)}}
		if len(pairCalls) > 0 && len(pairValid)*100 >= len(pairCalls)*80 {
			p.fingerprintConclusion(pair, pairRanking)
		}
	}
	if !coverage {
		item.Summary = fmt.Sprintf("有效样本不足（%d / %d），请检查请求明细后重测", len(valid), len(words))
		return item, pair
	}
	if len(distinct) > 1 {
		item.Status, item.Summary = Fail, "不同检测组的答案分布不一致："+strings.Join(labels, "、")
		return item, pair
	}
	if pair != nil && len(ranking) >= 2 {
		withinPair := func(model string) bool {
			for _, candidate := range p.pair.Models {
				if model == candidate {
					return true
				}
			}
			return false
		}
		if withinPair(ranking[0].Model) && withinPair(ranking[1].Model) {
			item.Status, item.Summary = pair.Status, pair.Summary
			return item, pair
		}
	}
	p.fingerprintConclusion(&item, ranking)
	return item, pair
}

func (p *plan) fingerprintConclusion(item *Item, ranking []Ranked) {
	leader, lead, clear := clearFingerprintLeader(ranking)
	switch {
	case len(ranking) < 2:
		item.Status, item.Summary = Warn, "可比型号样本不足"
	case leader.Distance > 0.45:
		item.Status, item.Summary = Warn, "答案分布与现有对照样本均有明显差异"
	case !clear:
		item.Status, item.Summary = Warn, fmt.Sprintf("%s 和 %s 难以区分", ranking[0].Model, ranking[1].Model)
	case leader.Model == p.spec.Baseline:
		item.Status, item.Summary = Pass, fmt.Sprintf("答案分布与 %s 一致（领先 %.3f）", leader.Model, float64(lead)/1000)
	default:
		item.Status, item.Summary = Fail, fmt.Sprintf("答案分布最像 %s（领先 %.3f）", leader.Model, float64(lead)/1000)
	}
}
