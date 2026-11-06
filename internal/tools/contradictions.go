package tools

// detect_contradictions：判断纪要里的可核验说法与实际数据是否矛盾
// （docs/design.md §10.3，移植自 packages/tools/src/contradictions.ts）。
// 模型只负责抽取说法（claims），判定完全由程序完成。

import (
	"math"
	"strconv"

	"digital-employee/internal/contract"
)

type resolvedSubject struct {
	level contract.ClaimSubjectLevel
	key   string
	// name 写进周报时用的展示名：区域名 / 门店名 / 品类名 / 全公司
	name string
	// wow 实际环比；nil 表示不可比（如第 36 周没有上周数据）
	wow *float64
}

// resolveSubject 按说法对象在事实里取值；对象在事实里找不到时返回 nil。
func resolveSubject(facts *contract.WeeklyFacts, subject contract.ClaimSubject) *resolvedSubject {
	switch subject.Level {
	case contract.ClaimCompany:
		return &resolvedSubject{level: contract.ClaimCompany, key: "company", name: "全公司", wow: facts.Company.Wow}
	case contract.ClaimRegion:
		m, ok := facts.Regions[subject.Key]
		if !ok {
			return nil
		}
		return &resolvedSubject{level: contract.ClaimRegion, key: subject.Key, name: subject.Key, wow: m.Wow}
	case contract.ClaimStore:
		for i := range facts.Stores {
			s := &facts.Stores[i]
			if s.Code == subject.Key {
				return &resolvedSubject{level: contract.ClaimStore, key: subject.Key, name: s.Name, wow: s.Wow}
			}
		}
		return nil
	case contract.ClaimCategory:
		for i := range facts.Categories {
			c := &facts.Categories[i]
			if c.Category == subject.Key {
				return &resolvedSubject{level: contract.ClaimCategory, key: subject.Key, name: c.Category, wow: c.Wow}
			}
		}
		return nil
	}
	return nil
}

// judge 判定一条说法是否构成矛盾。三条规则按顺序判，命中即返回：
//
//	flat       说“持平”，但 |实际环比| > flatTolerance
//	direction  说的方向与实际相反，且 |实际环比| > directionMin
//	magnitude  给出幅度时，|实际幅度 − 纪要幅度| > magnitudeTolerance
func judge(claim contract.Claim, actualWow float64, thresholds contract.ContradictionThresholds) contract.ContradictionRule {
	actualMagnitude := math.Abs(actualWow)
	if claim.Direction == contract.ClaimFlat && actualMagnitude > thresholds.FlatTolerance {
		return contract.RuleFlat
	}
	opposite := (claim.Direction == contract.ClaimUp && actualWow < 0) ||
		(claim.Direction == contract.ClaimDown && actualWow > 0)
	if opposite && actualMagnitude > thresholds.DirectionMin {
		return contract.RuleDirection
	}
	if claim.Magnitude != nil && math.Abs(actualMagnitude-*claim.Magnitude) > thresholds.MagnitudeTolerance {
		return contract.RuleMagnitude
	}
	return ""
}

// DetectContradictions 判定纪要矛盾。
func DetectContradictions(input contract.DetectContradictionsInput) (contract.DetectContradictionsOutput, error) {
	out := contract.DetectContradictionsOutput{Contradictions: make([]contract.DetectedContradiction, 0)}
	usedIDs := make(map[string]struct{}, len(input.Claims))
	for _, claim := range input.Claims {
		subject := resolveSubject(&input.Facts, claim.Subject)
		// 对象在事实里找不到（如客流、口感这类数据里没有的说法），
		// 或实际环比不可比，一律不判，跳过并计数。
		if subject == nil || subject.wow == nil {
			out.SkippedCount++
			continue
		}
		rule := judge(claim, *subject.wow, input.Thresholds)
		if rule == "" {
			continue
		}
		base := "contradiction:" + string(subject.level) + ":" + subject.key
		id := base
		for i := 2; ; i++ {
			if _, exists := usedIDs[id]; !exists {
				break
			}
			id = base + ":" + strconv.Itoa(i)
		}
		usedIDs[id] = struct{}{}
		detected := contract.DetectedContradiction{
			ID: id,
			Subject: contract.DetectedSubject{
				Level: subject.level,
				Key:   subject.key,
				Name:  subject.name,
			},
			Claim:      contract.DetectedClaim{Direction: claim.Direction, Magnitude: claim.Magnitude},
			ActualWow:  *subject.wow,
			Rule:       rule,
			SourceWeek: claim.SourceWeek,
			Quote:      claim.Quote,
		}
		out.Contradictions = append(out.Contradictions, detected)
	}
	return out, nil
}
