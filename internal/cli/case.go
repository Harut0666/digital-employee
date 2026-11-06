// 用例文件的解析与执行（对应 packages/cli/src/case.ts）。
// runCase 只依赖 contract.Engine：测试里用内存假引擎，命令行里用真实引擎。
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// caseActor 用例里可用的角色（system 只出现在事件里）。
var caseActors = []string{"initiator", "reviewer", "operator"}

var caseCommandTypes = []string{
	contract.CmdSubmit, contract.CmdSupplement, contract.CmdContinueWithoutMinutes,
	contract.CmdContinueWithPartial, contract.CmdRetry, contract.CmdTerminate,
	contract.CmdApprove, contract.CmdReturn,
}

// CaseExpectation 步骤级/顶层期望。
type CaseExpectation struct {
	State        string                  `json:"state,omitempty"`
	PauseCode    string                  `json:"pauseCode,omitempty"`
	Deliverables []contract.DeliverableKind `json:"deliverables,omitempty"`
	Checks       *CaseChecks             `json:"checks,omitempty"`
}

// StepExpectation 步骤级期望：在 CaseExpectation 之上加“命令是否应被接受”。
type StepExpectation struct {
	CaseExpectation
	OK    *bool  `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

// CaseStep 用例里的一条命令。
type CaseStep struct {
	Actor   contract.Role
	Command contract.TaskCommand
	Expect  *StepExpectation
}

// CaseDef 解析后的用例。
type CaseDef struct {
	ID          string
	Title       string
	Employee    string
	Form        map[string]any
	Attachments []contract.AttachmentInput
	Steps       []CaseStep
	Expect      *CaseExpectation
}

// StepResult 一条命令的执行结果（result.json 的 steps[]）。
type StepResult struct {
	Index     int    `json:"index"`
	Actor     string `json:"actor"`
	Command   string `json:"command"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Message   string `json:"message,omitempty"`
	State     string `json:"state,omitempty"`
	PauseCode string `json:"pauseCode,omitempty"`
	Failures  []string `json:"failures"`
}

// CaseResult run-case 的 result.json。
type CaseResult struct {
	CaseID       string                   `json:"caseId"`
	Title        string                   `json:"title"`
	Passed       bool                     `json:"passed"`
	TaskID       string                   `json:"taskId"`
	FinalState   contract.TaskState       `json:"finalState"`
	PauseCode    string                   `json:"pauseCode,omitempty"`
	Steps        []StepResult             `json:"steps"`
	Deliverables []contract.DeliverableRef `json:"deliverables"`
	Failures     []string                 `json:"failures"`
}

type rawAttachment struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type rawStep struct {
	Actor   string          `json:"actor"`
	Command json.RawMessage `json:"command"`
	Expect  *rawExpect      `json:"expect"`
}

type rawExpect struct {
	State        *string          `json:"state"`
	PauseCode    *string          `json:"pauseCode"`
	Deliverables []string         `json:"deliverables"`
	Checks       json.RawMessage  `json:"checks"`
	OK           *bool            `json:"ok"`
	Error        *string          `json:"error"`
}

type rawCase struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Employee    string          `json:"employee"`
	Form        map[string]any  `json:"form"`
	Attachments []rawAttachment `json:"attachments"`
	Steps       []rawStep       `json:"steps"`
	Expect      *rawExpect      `json:"expect"`
}

func caseFail(file, message string) error {
	return fmt.Errorf("用例文件无效（%s）：%s", file, message)
}

func parseAttachments(raw []rawAttachment, baseDir, file, where string) ([]contract.AttachmentInput, error) {
	out := make([]contract.AttachmentInput, 0, len(raw))
	for index, item := range raw {
		if item.Name == "" {
			return nil, caseFail(file, fmt.Sprintf("%s.attachments[%d] 缺少 name", where, index))
		}
		if item.Path == "" {
			return nil, caseFail(file, fmt.Sprintf("%s.attachments[%d] 缺少 path", where, index))
		}
		sourcePath := item.Path
		if !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(baseDir, sourcePath)
		}
		out = append(out, contract.AttachmentInput{Name: item.Name, SourcePath: sourcePath})
	}
	return out, nil
}

func convertExpectation(raw *rawExpect, file, where string) (*CaseExpectation, error) {
	if raw == nil {
		return nil, nil
	}
	expectation := &CaseExpectation{}
	if raw.State != nil {
		expectation.State = *raw.State
	}
	if raw.PauseCode != nil {
		expectation.PauseCode = *raw.PauseCode
	}
	for _, kind := range raw.Deliverables {
		expectation.Deliverables = append(expectation.Deliverables, contract.DeliverableKind(kind))
	}
	if len(raw.Checks) > 0 && string(raw.Checks) != "null" {
		checks, err := parseChecks(raw.Checks, where+".checks")
		if err != nil {
			return nil, err
		}
		expectation.Checks = &checks
	}
	return expectation, nil
}

// ParseCase 校验并规范化用例；相对路径按用例文件所在目录解析。
func ParseCase(raw []byte, baseDir, fileName string) (*CaseDef, error) {
	var parsed rawCase
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, caseFail(fileName, "不是合法的 JSON："+err.Error())
	}
	if parsed.ID == "" {
		return nil, caseFail(fileName, "缺少 id")
	}
	if parsed.Employee == "" {
		return nil, caseFail(fileName, "缺少 employee")
	}
	if len(parsed.Steps) == 0 {
		return nil, caseFail(fileName, "steps 必须是非空数组")
	}
	form := parsed.Form
	if form == nil {
		form = map[string]any{}
	}
	attachments, err := parseAttachments(parsed.Attachments, baseDir, fileName, "")
	if err != nil {
		return nil, err
	}
	def := &CaseDef{
		ID: parsed.ID, Title: parsed.Title, Employee: parsed.Employee,
		Form: form, Attachments: attachments,
	}
	if def.Title == "" {
		def.Title = parsed.ID
	}
	for index, rawStepValue := range parsed.Steps {
		where := fmt.Sprintf("steps[%d]", index)
		actor := rawStepValue.Actor
		if !containsString(caseActors, actor) {
			return nil, caseFail(fileName, where+".actor 必须是 initiator / reviewer / operator")
		}
		var commandRaw map[string]any
		if err := json.Unmarshal(rawStepValue.Command, &commandRaw); err != nil {
			return nil, caseFail(fileName, where+".command 必须是对象")
		}
		typeValue, _ := commandRaw["type"].(string)
		if !containsString(caseCommandTypes, typeValue) {
			return nil, caseFail(fileName, where+".command.type 必须是 "+strings.Join(caseCommandTypes, " / "))
		}
		if typeValue == contract.CmdReturn {
			comment, _ := commandRaw["comment"].(string)
			if comment == "" {
				return nil, caseFail(fileName, where+".command 的 return 必须有 comment")
			}
		}
		if rawStepValue.Expect != nil && len(rawStepValue.Expect.Checks) > 0 && string(rawStepValue.Expect.Checks) != "null" {
			return nil, caseFail(fileName, where+".expect 不支持 checks：判定要点只写在顶层 expect 里")
		}
		if typeValue == contract.CmdSupplement {
			if rawAttachments, present := commandRaw["attachments"]; present {
				encoded, _ := json.Marshal(rawAttachments)
				var items []rawAttachment
				if err := json.Unmarshal(encoded, &items); err != nil {
					return nil, caseFail(fileName, where+".command.attachments 必须是数组")
				}
				resolved, err := parseAttachments(items, baseDir, fileName, where+".command")
				if err != nil {
					return nil, err
				}
				encodedResolved, _ := json.Marshal(resolved)
				var resolvedRaw []any
				_ = json.Unmarshal(encodedResolved, &resolvedRaw)
				commandRaw["attachments"] = resolvedRaw
			}
		}
		commandData, err := json.Marshal(commandRaw)
		if err != nil {
			return nil, caseFail(fileName, where+".command 不是合法的 JSON")
		}
		var command contract.TaskCommand
		if err := json.Unmarshal(commandData, &command); err != nil {
			return nil, caseFail(fileName, where+".command 不是合法的 JSON")
		}
		expect, err := convertExpectation(rawStepValue.Expect, fileName, where+".expect")
		if err != nil {
			return nil, err
		}
		stepExpect := &StepExpectation{}
		if expect != nil {
			stepExpect.CaseExpectation = *expect
		}
		if rawStepValue.Expect != nil {
			stepExpect.OK = rawStepValue.Expect.OK
			if rawStepValue.Expect.Error != nil {
				stepExpect.Error = *rawStepValue.Expect.Error
			}
		} else {
			stepExpect = nil
		}
		def.Steps = append(def.Steps, CaseStep{Actor: contract.Role(actor), Command: command, Expect: stepExpect})
	}
	topExpect, err := convertExpectation(parsed.Expect, fileName, "expect")
	if err != nil {
		return nil, err
	}
	def.Expect = topExpect
	return def, nil
}

// ReadCaseFile 读并解析用例文件。
func ReadCaseFile(path string) (*CaseDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("找不到文件：%s", path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	return ParseCase(data, filepath.Dir(absolute), absolute)
}

// checkExpectation 检查快照是否满足期望；返回未通过项。
func checkExpectation(snapshot *contract.TaskSnapshot, expectation *CaseExpectation) []string {
	failures := []string{}
	if expectation == nil {
		return failures
	}
	if expectation.State != "" && string(snapshot.State) != expectation.State {
		failures = append(failures, fmt.Sprintf("期望状态 %s，实际 %s", expectation.State, snapshot.State))
	}
	if expectation.PauseCode != "" {
		actual := ""
		if snapshot.Pause != nil {
			actual = string(snapshot.Pause.Code)
		}
		if actual != expectation.PauseCode {
			if actual == "" {
				actual = "无"
			}
			failures = append(failures, fmt.Sprintf("期望暂停原因 %s，实际 %s", expectation.PauseCode, actual))
		}
	}
	for _, kind := range expectation.Deliverables {
		found := false
		for _, deliverable := range snapshot.Deliverables {
			if deliverable.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			failures = append(failures, fmt.Sprintf("期望有交付物 %s，实际没有", kind))
		}
	}
	return failures
}

// RunCaseOptions runCase 的输出目录。
type RunCaseOptions struct {
	OutDir string
}

// RunCase 按用例驱动引擎，写 snapshot.json / events.jsonl / result.json。
func RunCase(engine contract.Engine, def *CaseDef, options RunCaseOptions) (*CaseResult, error) {
	created, err := engine.Create(contract.CreateTaskInput{
		Employee: def.Employee, Form: def.Form, Attachments: def.Attachments, Actor: contract.RoleInitiator,
	})
	if err != nil {
		return nil, err
	}
	taskID := created.ID
	steps := []StepResult{}
	snapshot := created

	for index, step := range def.Steps {
		expectOK := true
		if step.Expect != nil && step.Expect.OK != nil {
			expectOK = *step.Expect.OK
		}
		result := engine.Command(taskID, step.Command, step.Actor)
		failures := []string{}
		stepResult := StepResult{
			Index: index, Actor: string(step.Actor), Command: step.Command.Type, OK: result.OK, Failures: failures,
		}
		if result.OK {
			settled, err := engine.WaitForSettled(taskID)
			if err != nil {
				stepResult.Failures = append(stepResult.Failures, "等待任务暂停/结束失败："+err.Error())
				steps = append(steps, stepResult)
				break
			}
			snapshot = settled
			if !expectOK {
				stepResult.Failures = append(stepResult.Failures, "期望命令被拒绝，实际被接受")
			}
		} else {
			stepResult.Error = result.Error
			stepResult.Message = result.Message
			if current, found := engine.Get(taskID); found {
				snapshot = current
			}
			if expectOK {
				stepResult.Failures = append(stepResult.Failures, fmt.Sprintf("命令被拒绝：%s（%s）", result.Error, result.Message))
			} else if step.Expect != nil && step.Expect.Error != "" && step.Expect.Error != result.Error {
				stepResult.Failures = append(stepResult.Failures, fmt.Sprintf("期望错误码 %s，实际 %s", step.Expect.Error, result.Error))
			}
		}
		stepResult.State = string(snapshot.State)
		if snapshot.Pause != nil {
			stepResult.PauseCode = string(snapshot.Pause.Code)
		}
		var expectation *CaseExpectation
		if step.Expect != nil {
			expectation = &step.Expect.CaseExpectation
		}
		stepResult.Failures = append(stepResult.Failures, checkExpectation(snapshot, expectation)...)
		steps = append(steps, stepResult)
		if len(stepResult.Failures) > 0 {
			break
		}
	}

	stepFailures := []string{}
	for _, step := range steps {
		for _, failure := range step.Failures {
			stepFailures = append(stepFailures, fmt.Sprintf("第 %d 步：%s", step.Index+1, failure))
		}
	}
	finalFailures := []string{}
	if len(steps) == len(def.Steps) && len(stepFailures) == 0 {
		finalFailures = checkExpectation(snapshot, def.Expect)
	}
	failures := append(stepFailures, finalFailures...)
	outcome := &CaseResult{
		CaseID: def.ID, Title: def.Title, Passed: len(failures) == 0, TaskID: taskID,
		FinalState: snapshot.State, Steps: steps,
		Deliverables: snapshot.Deliverables, Failures: failures,
	}
	if snapshot.Pause != nil {
		outcome.PauseCode = string(snapshot.Pause.Code)
	}
	if outcome.Deliverables == nil {
		outcome.Deliverables = []contract.DeliverableRef{}
	}
	if outcome.Failures == nil {
		outcome.Failures = []string{}
	}

	events, err := engine.Events(taskID, 0)
	if err != nil {
		return nil, err
	}
	if err := writeRunArtifacts(options.OutDir, snapshot, events, outcome); err != nil {
		return nil, err
	}
	return outcome, nil
}

func writeRunArtifacts(outDir string, snapshot *contract.TaskSnapshot, events []contract.TaskEvent, result *CaseResult) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	snapshotData, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "snapshot.json"), snapshotData, 0o644); err != nil {
		return err
	}
	lines := make([]string, 0, len(events))
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		lines = append(lines, string(data))
	}
	eventsText := ""
	if len(lines) > 0 {
		eventsText = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(outDir, "events.jsonl"), []byte(eventsText), 0o644); err != nil {
		return err
	}
	resultData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "result.json"), resultData, 0o644)
}
