// 草稿文本的收集与裸数字扫描（移植自 packages/core/src/qc/text.ts）。
package qc

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"digital-employee/internal/contract"
)

// TextPiece 一段叙述文本，Where 是问题描述里用的位置（如 “本周摘要”）。
type TextPiece struct {
	Where string
	Text  string
}

// NarrativePieces 叙述文本：标题、各章节的段落和要点、异常原因说明。
// 不含原因摘录 quote（那是纪要原文，允许带数字）和修改说明。
func NarrativePieces(draft contract.DraftReport) []TextPiece {
	pieces := []TextPiece{{Where: "标题", Text: draft.Title}}
	for _, section := range draft.Sections {
		for _, paragraph := range section.Paragraphs {
			pieces = append(pieces, TextPiece{Where: section.Heading, Text: paragraph})
		}
		for _, bullet := range section.Bullets {
			pieces = append(pieces, TextPiece{Where: section.Heading, Text: bullet})
		}
	}
	for _, cause := range draft.Causes {
		pieces = append(pieces, TextPiece{Where: "异常原因（" + cause.AnomalyID + "）", Text: cause.Cause})
	}
	return pieces
}

// bareNumberWhitelist 裸数字白名单：这些数字不是“事实”，允许直接出现在叙述里。
// 顺序与 TS 版一致（斜杠日期用 maskSlashDates 手工实现，见下）。
var bareNumberWhitelist = []*regexp.Regexp{
	regexp.MustCompile(`[A-Za-z][A-Za-z0-9_-]*\d[A-Za-z0-9_-]*`),
	regexp.MustCompile(`\d{4}-(W\d{1,2}|\d{1,2}-\d{1,2})`),
	regexp.MustCompile(`\d{4}\s*年`),
	regexp.MustCompile(`第?\s*\d+\s*周`),
	regexp.MustCompile(`\d{1,2}\s*月(\s*\d{1,2}\s*[日号])?`),
	regexp.MustCompile(`\d{1,2}\s*[日号]`),
	regexp.MustCompile(`前\s*\d+`),
	regexp.MustCompile(`^\s*[（(]?\d+[)）.、]`),
}

// 斜杠日期在 TS 里是白名单的第 7 条（下标 6），单独用 maskSlashDates 处理。
const slashDateWhitelistIndex = 6

var bareNumber = regexp.MustCompile(`[+\-−]?\d[\d,]*(\.\d+)?\s*[%％]?`)

var quoteOpeners = map[rune]bool{'“': true, '「': true}
var quoteClosers = map[rune]rune{'”': '“', '」': '「'}

// MaskQuotedSpans 把落在引号“…”「…」内、且内容逐字出自 quotes 之一的片段抹掉（替换为空格）。
// 引号可以嵌套；外层或内层任一层满足条件即抹掉。
func MaskQuotedSpans(text string, quotes []string) string {
	if len(quotes) == 0 {
		return text
	}
	runes := []rune(text)
	type span struct {
		open  rune
		index int
	}
	stack := []span{}
	type pair struct{ from, to int }
	ranges := []pair{}
	for index, char := range runes {
		if quoteOpeners[char] {
			stack = append(stack, span{open: char, index: index})
			continue
		}
		closer, isCloser := quoteClosers[char]
		if !isCloser {
			continue
		}
		if len(stack) == 0 || stack[len(stack)-1].open != closer {
			continue
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		inner := strings.TrimSpace(string(runes[top.index+1 : index]))
		if inner != "" && anyContains(quotes, inner) {
			ranges = append(ranges, pair{from: top.index, to: index + 1})
		}
	}
	if len(ranges) == 0 {
		return text
	}
	masked := []rune(text)
	for _, r := range ranges {
		for index := r.from; index < r.to && index < len(masked); index++ {
			masked[index] = ' '
		}
	}
	return string(masked)
}

func anyContains(haystacks []string, needle string) bool {
	for _, haystack := range haystacks {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

// EntityNamesWithDigits 事实里名称带数字的实体（如单品“6寸生日蛋糕”），名称本身不是数字事实。
// 长名称在前，避免被短名称截断。
func EntityNamesWithDigits(facts contract.WeeklyFacts) []string {
	names := []string{}
	for _, product := range facts.TopProducts {
		names = append(names, product.Name)
	}
	for _, store := range facts.Stores {
		names = append(names, store.Name)
	}
	for _, category := range facts.Categories {
		names = append(names, category.Category)
	}
	for region := range facts.Regions {
		names = append(names, region)
	}
	for _, anomaly := range facts.Anomalies {
		names = append(names, anomaly.Name)
	}
	seen := map[string]bool{}
	kept := []string{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if containsDigit(name) {
			kept = append(kept, name)
		}
	}
	sort.SliceStable(kept, func(a, b int) bool { return len([]rune(kept[a])) > len([]rune(kept[b])) })
	return kept
}

func containsDigit(text string) bool {
	for _, char := range text {
		if char >= '0' && char <= '9' {
			return true
		}
	}
	return false
}

// FindBareNumbers 返回文本中槽位之外、也不在白名单内的裸数字（原样片段）。
//
// quotes 是各条原因的纪要摘录；引号内逐字出自它们的数字视为引用原文，不算裸数字。
// entityNames 是事实里带数字的实体名称，出现时整体抹掉。
func FindBareNumbers(text string, quotes []string, entityNames []string) []string {
	masked := MaskQuotedSpans(text, quotes)
	masked = SlotPattern.ReplaceAllString(masked, " ")
	for _, name := range entityNames {
		masked = strings.ReplaceAll(masked, name, " ")
	}
	for _, pattern := range bareNumberWhitelist {
		masked = pattern.ReplaceAllString(masked, " ")
	}
	masked = maskSlashDates(masked)
	matches := bareNumber.FindAllString(masked, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, strings.TrimSpace(match))
	}
	return out
}

// maskSlashDates 抹掉“10/27”“11/03”这类月/日写法（等价于 JS 的
// /(?<![\d.])\d{1,2}\s*\/\s*\d{1,2}(?![\d.\/])/g —— Go 的 RE2 不支持前后瞻，手工实现）。
func maskSlashDates(text string) string {
	runes := []rune(text)
	if !hasRune(runes, '/') {
		return text
	}
	out := append([]rune(nil), runes...)
	isDigit := func(char rune) bool { return char >= '0' && char <= '9' }
	isSpace := func(char rune) bool { return unicode.IsSpace(char) }

	for start := 0; start < len(runes); {
		if !isDigit(runes[start]) {
			start++
			continue
		}
		// 左边界：不能紧跟数字或点
		if start > 0 && (isDigit(runes[start-1]) || runes[start-1] == '.') {
			start++
			continue
		}
		matched := -1
		for firstDigits := 2; firstDigits >= 1; firstDigits-- {
			cursor := start + firstDigits
			if cursor > len(runes) {
				continue
			}
			allDigits := true
			for index := start; index < cursor; index++ {
				if !isDigit(runes[index]) {
					allDigits = false
					break
				}
			}
			if !allDigits {
				continue
			}
			for cursor < len(runes) && isSpace(runes[cursor]) {
				cursor++
			}
			if cursor >= len(runes) || runes[cursor] != '/' {
				continue
			}
			cursor++
			for cursor < len(runes) && isSpace(runes[cursor]) {
				cursor++
			}
			for secondDigits := 2; secondDigits >= 1; secondDigits-- {
				end := cursor + secondDigits
				if end > len(runes) {
					continue
				}
				allDigits = true
				for index := cursor; index < end; index++ {
					if !isDigit(runes[index]) {
						allDigits = false
						break
					}
				}
				if !allDigits {
					continue
				}
				// 右边界：不能紧跟数字、点或斜杠
				if end < len(runes) && (isDigit(runes[end]) || runes[end] == '.' || runes[end] == '/') {
					continue
				}
				matched = end
				break
			}
			if matched >= 0 {
				break
			}
		}
		if matched < 0 {
			start++
			continue
		}
		for index := start; index < matched; index++ {
			out[index] = ' '
		}
		start = matched
	}
	return string(out)
}

func hasRune(runes []rune, want rune) bool {
	for _, char := range runes {
		if char == want {
			return true
		}
	}
	return false
}

var sentenceSplitters = []string{"。", "！", "？", "!", "?"}

// CountSentences 按句末标点切句，空句不计。
func CountSentences(paragraphs []string) int {
	total := 0
	for _, paragraph := range paragraphs {
		parts := []string{paragraph}
		for _, splitter := range sentenceSplitters {
			next := make([]string, 0, len(parts))
			for _, part := range parts {
				next = append(next, strings.Split(part, splitter)...)
			}
			parts = next
		}
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				total++
			}
		}
	}
	return total
}
