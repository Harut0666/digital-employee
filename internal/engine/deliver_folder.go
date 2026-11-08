// 交付物写入目录（Spec 0006“交付物写入目录”、T42）。
//
// 每个版本进入待审核时，把这一版的交付物（Word 或 Markdown，不含检验记录）复制到所属工作区目录的
// “交付”文件夹里，文件名是 <员工名>_<统计周期或会话标题>_v<版本号>.<扩展名>。
// 同名文件已存在时加序号（v1-2、v1-3……），不覆盖；写不进去不影响任务，只在轨迹里记一条说明事件。
// 任务目录里的版本仍是正本，下载接口不变。
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

// deliverDirName 工作区目录里放交付物的文件夹（与 app.DeliverDirName 一致；引擎不依赖 app，避免循环引用）。
const deliverDirName = "交付"

// fileNameUnsafe 文件名里不能出现的字符：员工名和表单字段都是人填的，填出来的名字不能变成路径。
var fileNameUnsafe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// fileDeliverables 把一个版本的交付物复制进工作区目录的“交付”文件夹，并在轨迹里记一条说明
// （写不进去也只记说明，不影响任务）。任务没记下工作区、工作区查不到目录或者单目录模式下不写。
// 只在进入待审核之后调用：rt.mu 不能已经被持有。
func (e *Engine) fileDeliverables(rt *taskRuntime) {
	workspaceID := view(rt, func(p *projection) string { return p.WorkspaceID })
	dir, ok := e.workspaceDir(workspaceID)
	if !ok || dir == "" {
		return
	}
	target := filepath.Join(dir, deliverDirName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		e.appendFiledNotice(rt, contract.TaskEvent{
			Type:        contract.EventDeliverableFiled,
			FiledReason: fmt.Sprintf("没能把交付物复制到工作区目录的“%s”文件夹：%v。交付物仍在任务目录里，可以照常下载。", deliverDirName, err),
			FiledError:  err.Error(),
		})
		return
	}

	version := view(rt, func(p *projection) int {
		if len(p.versions) > 0 {
			return len(p.versions)
		}
		return p.ReworkRound + 1
	})
	label := e.filedLabel(rt)
	for _, ref := range view(rt, func(p *projection) []contract.DeliverableRef { return p.Deliverables() }) {
		if ref.Kind != contract.DeliverableDocx && ref.Kind != contract.DeliverableMD {
			continue
		}
		path, err := fileDeliverable(target, ref, label, version)
		if err != nil {
			e.appendFiledNotice(rt, contract.TaskEvent{
				Type:        contract.EventDeliverableFiled,
				FiledReason: fmt.Sprintf("没能把第 %d 版的交付物复制到“%s”文件夹：%v。交付物仍在任务目录里，可以照常下载。", version, deliverDirName, err),
				FiledError:  err.Error(),
			})
			continue
		}
		e.appendFiledNotice(rt, contract.TaskEvent{
			Type:        contract.EventDeliverableFiled,
			FiledPath:   path,
			FiledReason: "交付物已复制到 " + path,
		})
	}
}

func (e *Engine) appendFiledNotice(rt *taskRuntime, event contract.TaskEvent) {
	// 写不进去不影响任务：说明事件本身也写不进去（比如日志已不可写）时只能放弃
	_ = e.append(rt, event)
}

// fileDeliverable 把一个交付物复制到交付文件夹，返回实际写入的路径；同名时加序号，不覆盖。
func fileDeliverable(dir string, ref contract.DeliverableRef, label string, version int) (string, error) {
	extension := strings.ToLower(filepath.Ext(ref.Path))
	if extension == "" {
		extension = "." + string(ref.Kind)
	}
	stem := fmt.Sprintf("%s_v%d", label, version)
	if label == "" {
		stem = fmt.Sprintf("v%d", version)
	}
	// 先用“不覆盖地新建”抢到文件名，再写内容：并发写入时也不会覆盖别人的文件
	path := filepath.Join(dir, stem+extension)
	for index := 2; ; index++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			source, readErr := os.ReadFile(ref.Path)
			if readErr == nil {
				_, readErr = file.Write(source)
			}
			closeErr := file.Close()
			if readErr == nil {
				readErr = closeErr
			}
			if readErr != nil {
				_ = os.Remove(path)
				return "", readErr
			}
			return path, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, index, extension))
	}
}

// filedLabel 文件名里的“<员工名>_<统计周期或会话标题>”：员工名取员工配置的名字（没有时用员工 id），
// 统计周期取员工配置的交付名模板（场景 A 是 经营周报_{year}年第{week}周，去掉与员工名重复的开头后是
// 2026年第44周）；模板填不完整时用表单里的统计周期。
func (e *Engine) filedLabel(rt *taskRuntime) string {
	employee := cleanFileName(rt.config.Workflow.Name)
	if employee == "" {
		employee = view(rt, func(p *projection) string { return p.Employee })
	}
	subject := ""
	if template := rt.config.Workflow.DeliverableName; template != "" {
		// 模板里带着员工的说法（经营周报_{year}年第{week}周）：员工名已经写在前面，去掉开头的同名前缀，
		// 只留统计周期部分；填不上时再看统计周期
		if filled, complete := contract.FillNameTemplate(template, view(rt, func(p *projection) map[string]any { return p.Form })); complete {
			subject = trimNamePrefix(filled, employee)
		}
	}
	if subject == "" {
		subject = periodText(view(rt, func(p *projection) map[string]any { return p.Form }))
	}
	if subject == "" && employee == "" {
		return ""
	}
	return employee + "_" + subject
}

// trimNamePrefix 名字开头盖住这句话（如统计周期）的部分：经营周报员工_经营周报_2026年第44周 里的 经营周报
// 已经整个被前面的员工名盖住，再去掉（→ 员工名_2026年第44周）；不重叠的不动，去掉后什么都不剩时保留原样。
func trimNamePrefix(name, employee string) string {
	trimmed := name
	for length := len([]rune(employee)); length > 0; length-- {
		head := string([]rune(employee)[:length])
		if !strings.HasPrefix(name, head) {
			continue
		}
		// 只去掉一个分隔符：经营周报_2026年第44周 里只去掉员工名和“_”，不碰统计周期里的“第”
		rest := strings.TrimPrefix(strings.TrimPrefix(name, head), "_")
		rest = strings.TrimPrefix(rest, " ")
		if strings.Trim(rest, "_. ") != "" && len(rest) < len(trimmed) {
			trimmed = rest
		}
	}
	return trimmed
}

// periodText 表单里的统计周期，如 2026年第44周；没有完整的年月周时为空。
func periodText(form map[string]any) string {
	year, week := 0, 0
	if period, ok := form["period"].(map[string]any); ok {
		year, week = periodNumbers(period, year, week)
	}
	year, week = periodNumbers(form, year, week)
	if year == 0 || week == 0 {
		return ""
	}
	return fmt.Sprintf("%d年第%s周", year, formatInt(week))
}

func periodNumbers(fields map[string]any, year, week int) (int, int) {
	if value, ok := asFloat(fields["year"]); ok && value > 0 {
		year = int(value)
	}
	if value, ok := asFloat(fields["week"]); ok && value > 0 {
		week = int(value)
	}
	return year, week
}

func formatInt(value int) string { return strconv.Itoa(value) }

// cleanFileName 去掉文件名里不能用的字符；为空或只剩点和空格时返回空串。
func cleanFileName(text string) string {
	cleaned := strings.TrimRight(fileNameUnsafe.ReplaceAllString(strings.TrimSpace(text), "_"), ". ")
	if strings.Trim(cleaned, "_. ") == "" {
		return ""
	}
	return cleaned
}
