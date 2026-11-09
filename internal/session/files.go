// 前台 Agent 的文件写入工具（T48）：write_file 整文件写入，edit_file 换掉一段恰好出现一次的原文。
//
// 它们和读目录的工具一样只碰工作区目录，但多了始终适用的边界，不受权限模式影响：
// 写操作只限工作区目录内（路径逐段解析，链接先解开）；隐藏文件、“交付”和“材料”文件夹不可写；
// 单个文件最多 1 MB，只写文本。边界检查在 guard 里，先于权限模式；权限模式再决定放行、询问或拒绝。
package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// maxWriteBytes 单个文件最多写多少字节。
const maxWriteBytes = 1 << 20

// writeForbiddenDirs 工作区目录里程序自己写入的文件夹：Agent 不能写。
var writeForbiddenDirs = map[string]string{
	"交付": "“交付”文件夹由程序写入交付物，Agent 不能写",
	"材料": "“材料”文件夹存放上传的材料，Agent 不能写",
}

// canonicalSegment 路径段在 Windows 上实际指向的名字：段尾的点和空格会被悄悄去掉，大小写不敏感。
// 比较保留文件夹时一律用它，写法再绕也认得出来。
func canonicalSegment(segment string) string {
	return strings.ToLower(strings.TrimRight(segment, ". "))
}

// forbiddenDir 这一段是不是程序自己写入的文件夹（“交付”“材料”）；是则返回说明。
func forbiddenDir(segment string) (string, bool) {
	why, forbidden := writeForbiddenDirs[canonicalSegment(segment)]
	return why, forbidden
}

// windowsDevices Windows 的保留设备名：不论带不带扩展名，都不是普通文件。
var windowsDevices = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true, "conin$": true, "conout$": true,
}

// isWindowsDevice CON、NUL、COM1~9、LPT1~9（含上标数字）等设备名，带扩展名（nul.txt）也算。
func isWindowsDevice(segment string) bool {
	stem := strings.ToLower(strings.TrimRight(segment, ". "))
	if dot := strings.Index(stem, "."); dot >= 0 {
		stem = strings.TrimRight(stem[:dot], " ")
	}
	if windowsDevices[stem] {
		return true
	}
	runes := []rune(stem)
	if len(runes) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) {
		switch last := runes[3]; {
		case last >= '1' && last <= '9', last == '¹', last == '²', last == '³':
			return true
		}
	}
	return false
}

// unwritableSegment 这一段的写法本身就不能写：Windows 会把它变成别的东西（去掉段尾的点和空格、
// 把冒号当数据流、把 CON/NUL 当设备），检查和实际写到的位置就对不上了。返回拒绝原因，合格返回空串。
func unwritableSegment(segment string) string {
	switch {
	case strings.Contains(segment, ":"):
		return fmt.Sprintf("路径里的“%s”含有冒号（Windows 的数据流写法），不能写", segment)
	case strings.ContainsAny(segment, "<>\"|?*") || strings.IndexFunc(segment, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
		return fmt.Sprintf("文件名“%s”里不能含有 < > \" | ? * 或控制字符", segment)
	case segment != ".." && strings.TrimRight(segment, ". ") != segment:
		return fmt.Sprintf("路径里的“%s”以点或空格结尾，Windows 会悄悄去掉它们，不能写", segment)
	case isWindowsDevice(segment):
		return fmt.Sprintf("“%s”是 Windows 的设备名，不是文件，不能写", segment)
	}
	return ""
}

// writeTarget 一次写操作要写的文件：真实路径和相对工作区目录的路径（/ 分隔）。
type writeTarget struct {
	path string
	rel  string
}

// resolveWritable 把 Agent 给的路径解析成一个允许写的位置；不允许的一律返回 denial。
func (t *turn) resolveWritable(raw string) (writeTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return writeTarget{}, denied("缺少 path：要写哪个文件（相对工作区目录的路径）。")
	}
	root := t.workspaceDir()
	if root == "" {
		return writeTarget{}, denied("工作区目录不存在，没有地方可写。")
	}
	// 先看写法：Windows 会悄悄改写的路径段（段尾的点和空格、冒号、设备名）不用解析，直接拒绝。
	// 否则“交付”“材料”还不存在时，“材料./x.md”能躲过名字比较，被系统建成“材料”
	requested, segmentsErr := relativeSegments(raw)
	if segmentsErr != nil {
		return writeTarget{}, denied("%s。我只能写工作区目录里的文件。", segmentsErr.Error())
	}
	for _, segment := range requested {
		if reason := unwritableSegment(segment); reason != "" {
			return writeTarget{}, denied("%s：%s", reason, raw)
		}
	}
	target, rel, err := resolveInWorkspace(root, raw)
	if err != nil {
		return writeTarget{}, denied("%s。我只能写工作区目录里的文件。", err.Error())
	}
	if rel == "." {
		return writeTarget{}, denied("这是工作区目录本身，不是文件：%s", raw)
	}
	// 请求的路径和解开链接之后的路径都不能带隐藏的一段；解开之后的路径同样不能是奇怪的写法
	resolved := strings.Split(rel, "/")
	for _, segment := range append(append([]string{}, requested...), resolved...) {
		if segment != ".." && hiddenName(segment) {
			return writeTarget{}, denied("不能写隐藏文件或隐藏文件夹：%s", raw)
		}
	}
	for _, segment := range resolved {
		if reason := unwritableSegment(segment); reason != "" {
			return writeTarget{}, denied("%s：%s", reason, raw)
		}
	}
	// 保留文件夹按规范化之后的名字比较：先看解开之后的路径，再看请求的第一段
	if why, forbidden := forbiddenDir(resolved[0]); forbidden {
		return writeTarget{}, denied("%s：%s", why, raw)
	}
	if err := rejectDanglingLinks(root, rel); err != nil {
		return writeTarget{}, err
	}
	return writeTarget{path: target, rel: rel}, nil
}

// rejectDanglingLinks 路径上还留着的链接都是解不开的（指向不存在的位置）：写下去会跟着链接跑到别处，不允许。
func rejectDanglingLinks(root, rel string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return denied("工作区目录不可用：%v", err)
	}
	current := rootAbs
	for _, segment := range strings.Split(rel, "/") {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			// 这一段还不存在，后面的也不存在
			return nil
		}
		if info.Mode()&fs.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return denied("路径上有一个指向不存在位置的链接，不能写：%s", rel)
		}
	}
	return nil
}

// checkContent 写进去的内容：只写文本，最多 1 MB。
func checkContent(content string) error {
	if len(content) > maxWriteBytes {
		return denied("单个文件最多写 1 MB，这次要写 %d 字节。", len(content))
	}
	if strings.ContainsRune(content, 0) || !utf8.ValidString(content) {
		return denied("只能写文本内容，这段内容含有二进制数据。")
	}
	return nil
}

// planWrite write_file 的边界检查：路径和内容都合格才返回目标。
func (t *turn) planWrite(arguments map[string]any) (writeTarget, string, error) {
	requested, _ := arguments["path"].(string)
	target, err := t.resolveWritable(requested)
	if err != nil {
		return writeTarget{}, "", err
	}
	content, ok := arguments["content"].(string)
	if !ok {
		return writeTarget{}, "", denied("缺少 content：要写进文件的文本。")
	}
	if err := checkContent(content); err != nil {
		return writeTarget{}, "", err
	}
	return target, content, nil
}

// planEdit edit_file 的边界检查：路径合格、原文不空。
func (t *turn) planEdit(arguments map[string]any) (writeTarget, string, string, error) {
	requested, _ := arguments["path"].(string)
	target, err := t.resolveWritable(requested)
	if err != nil {
		return writeTarget{}, "", "", err
	}
	oldText, _ := arguments["oldText"].(string)
	newText, hasNew := arguments["newText"].(string)
	if oldText == "" {
		return writeTarget{}, "", "", denied("缺少 oldText：要被换掉的原文（必须在文件里恰好出现一次）。")
	}
	if !hasNew {
		return writeTarget{}, "", "", denied("缺少 newText：换成的新文本（要删掉这段就给空串）。")
	}
	if err := checkContent(newText); err != nil {
		return writeTarget{}, "", "", err
	}
	return target, oldText, newText, nil
}

// objectOf 权限确认卡上写的对象：相对工作区目录的路径，统一用 /。
func objectOf(arguments map[string]any) string {
	requested, _ := arguments["path"].(string)
	return strings.Trim(strings.ReplaceAll(strings.TrimSpace(requested), `\`, "/"), "/")
}

// snippet 压成一行、最多 30 个字，用在一句话说明里。
func snippet(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 30 {
		return string(runes[:30]) + "…"
	}
	return text
}

// fileWriteTools 前台 Agent 的文件写入工具（write_file、edit_file）。动作类别是“写入文件”，要过权限关口（先看边界，再看权限模式和说话人的角色）。
func (t *turn) fileWriteTools() []tool {
	write := tool{
		spec: agent.ToolSpec{
			Name:        contract.ToolWriteFile,
			Description: "把文本整个写进工作区目录里的一个文件：不存在就新建（缺的文件夹一并建），已存在就覆盖。只写文本，最多 1 MB；不能写隐藏文件，也不能写“交付”和“材料”文件夹。",
			Parameters: map[string]any{"type": "object", "required": []any{"path", "content"}, "properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "文件路径，相对工作区目录，如 notes/周会要点.md"},
				"content": map[string]any{"type": "string", "description": "要写进文件的全部文本"},
			}},
		},
		action: ActionWriteFile,
		guard: func(arguments map[string]any) error {
			_, _, err := t.planWrite(arguments)
			return err
		},
		describe: func(arguments map[string]any) (string, string) {
			content, _ := arguments["content"].(string)
			requested, _ := arguments["path"].(string)
			if target, err := t.resolveWritable(requested); err == nil {
				if info, statErr := os.Stat(target.path); statErr == nil && info.Mode().IsRegular() {
					return objectOf(arguments), fmt.Sprintf("覆盖已有文件，写入 %d 字节", len(content))
				}
			}
			return objectOf(arguments), fmt.Sprintf("新建文件，写入 %d 字节", len(content))
		},
		run: func(arguments map[string]any) (any, error) {
			// 人可能等了很久才点允许：边界要再检查一遍
			target, content, err := t.planWrite(arguments)
			if err != nil {
				return nil, err
			}
			action := "created"
			if info, statErr := os.Stat(target.path); statErr == nil {
				if info.IsDir() {
					return nil, toolFailure("is_directory", "%s 是一个文件夹，不能当文件写。", target.rel)
				}
				action = "modified"
			}
			if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
				return nil, fmt.Errorf("建不了文件夹：%v", err)
			}
			if err := os.WriteFile(target.path, []byte(content), 0o644); err != nil {
				return nil, fmt.Errorf("写不进 %s：%v", target.rel, err)
			}
			return map[string]any{"path": target.rel, "action": action, "bytes": len(content)}, nil
		},
	}
	edit := tool{
		spec: agent.ToolSpec{
			Name:        contract.ToolEditFile,
			Description: "把工作区目录里一个已有文本文件中的一段原文换成新文本。原文必须在文件里恰好出现一次：找不到或有多处都会报错（并说明是哪种），这时先 read_file 看清内容，再带上足够的上下文让原文唯一。",
			Parameters: map[string]any{"type": "object", "required": []any{"path", "oldText", "newText"}, "properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "文件路径，相对工作区目录"},
				"oldText": map[string]any{"type": "string", "description": "要被换掉的原文，必须与文件里的一字不差，且恰好出现一次"},
				"newText": map[string]any{"type": "string", "description": "换成的新文本；要删掉这段就给空串"},
			}},
		},
		action: ActionWriteFile,
		guard: func(arguments map[string]any) error {
			_, _, _, err := t.planEdit(arguments)
			return err
		},
		describe: func(arguments map[string]any) (string, string) {
			oldText, _ := arguments["oldText"].(string)
			newText, _ := arguments["newText"].(string)
			return objectOf(arguments), fmt.Sprintf("修改文件：把「%s」换成「%s」", snippet(oldText), snippet(newText))
		},
		run: func(arguments map[string]any) (any, error) {
			target, oldText, newText, err := t.planEdit(arguments)
			if err != nil {
				return nil, err
			}
			info, statErr := os.Stat(target.path)
			switch {
			case errors.Is(statErr, fs.ErrNotExist):
				return nil, toolFailure("file_missing", "文件不存在：%s。要新建文件请用 write_file。", target.rel)
			case statErr != nil:
				return nil, fmt.Errorf("读不了 %s：%v", target.rel, statErr)
			case !info.Mode().IsRegular():
				return nil, toolFailure("not_a_file", "%s 不是文件。", target.rel)
			case info.Size() > maxWriteBytes:
				return nil, denied("%s 超过 1 MB，不能用 edit_file 改。", target.rel)
			}
			raw, err := os.ReadFile(target.path)
			if err != nil {
				return nil, fmt.Errorf("读不了 %s：%v", target.rel, err)
			}
			if err := checkContent(string(raw)); err != nil {
				return nil, denied("%s 不是文本文件，不能用 edit_file 改。", target.rel)
			}
			content := string(raw)
			switch count := strings.Count(content, oldText); {
			case count == 0:
				return nil, toolFailure("not_found", "在 %s 里没有找到要替换的原文。请先 read_file 看清文件内容，原文要与文件里的一字不差（含空格和换行）。", target.rel)
			case count > 1:
				return nil, toolFailure("ambiguous", "要替换的原文在 %s 里出现了 %d 处，需要恰好一处。请多带一些前后的内容，让原文唯一。", target.rel, count)
			}
			if oldText == newText {
				return nil, toolFailure("no_change", "新文本和原文相同，没有改动。")
			}
			at := strings.Index(content, oldText)
			updated := content[:at] + newText + content[at+len(oldText):]
			if err := checkContent(updated); err != nil {
				return nil, err
			}
			if err := os.WriteFile(target.path, []byte(updated), 0o644); err != nil {
				return nil, fmt.Errorf("写不进 %s：%v", target.rel, err)
			}
			return map[string]any{
				"path": target.rel, "action": "modified", "bytes": len(updated),
				"diff": editDiff(content, at, oldText, newText),
			}, nil
		},
	}
	return []tool{write, edit}
}

// diffLines 一个改动块最多显示多少行。
const diffLines = 20

// editDiff 改动前后的差异摘要：整行对照，- 开头是原来的，+ 开头是现在的。
func editDiff(content string, at int, oldText, newText string) string {
	start := strings.LastIndex(content[:at], "\n") + 1
	end := len(content)
	if next := strings.Index(content[at+len(oldText):], "\n"); next >= 0 {
		end = at + len(oldText) + next
	}
	before, after := content[start:at], content[at+len(oldText):end]
	line := strings.Count(content[:at], "\n") + 1
	return fmt.Sprintf("@@ 第 %d 行 @@\n%s\n%s", line, signed("-", before+oldText+after), signed("+", before+newText+after))
}

// signed 每一行前加符号；太长的块只留前几行。
func signed(sign, block string) string {
	lines := strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n")
	omitted := 0
	if len(lines) > diffLines {
		omitted = len(lines) - diffLines
		lines = lines[:diffLines]
	}
	for index := range lines {
		lines[index] = sign + lines[index]
	}
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("%s…（还有 %d 行）", sign, omitted))
	}
	return strings.Join(lines, "\n")
}
