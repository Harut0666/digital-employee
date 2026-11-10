package literature

import (
	"fmt"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// ValidateInput validate_literature_submission 的入参。
type ValidateInput struct {
	Direction   string                   `json:"direction"`
	Attachments []contract.AttachmentRef `json:"attachments"`
	EmployeeDir string                   `json:"employeeDir"`
}

// ListFile 识别出的专利清单。
type ListFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Rows int    `json:"rows"`
}

// ValidateOutput 准入校验结果：不通过时 Pause 给出最根本的一个原因。
type ValidateOutput struct {
	OK    bool      `json:"ok"`
	Pause *Gate     `json:"pause,omitempty"`
	List  *ListFile `json:"list,omitempty"`
}

func fail(code, message string, detail map[string]any) ValidateOutput {
	return ValidateOutput{OK: false, Pause: &Gate{Code: code, Message: message, Detail: detail}}
}

// Validate 表单与附件校验：技术方向要填，要有一份能读的专利清单（CSV，必须有“专利编号”列）。
// 只报最根本的一个原因，不替发起人编造清单。
func Validate(in ValidateInput) (ValidateOutput, error) {
	if strings.TrimSpace(in.Direction) == "" {
		return fail(string(contract.PauseInvalidInput), "缺少技术方向：请填写要研判的技术方向，例如“固态电池电解质”。", map[string]any{"missing": []string{"技术方向"}}), nil
	}
	var pick *contract.AttachmentRef
	for i := len(in.Attachments) - 1; i >= 0; i-- { // 补交的更正文件追加在末尾，取最后一份
		ref := in.Attachments[i]
		name := ref.Name
		if name == "" {
			name = ref.Path
		}
		if strings.EqualFold(filepath.Ext(name), ".csv") {
			pick = &ref
			break
		}
	}
	if pick == nil {
		return fail(string(contract.PauseMissingList),
			"缺少专利清单：未找到 CSV 格式的专利清单，无法开始研判。请上传业务方提供的专利清单（CSV，含“专利编号”列）后继续。",
			map[string]any{"missing": []string{"专利清单"}}), nil
	}
	rows, err := ReadList(pick.Path)
	displayName := pick.Name
	if displayName == "" {
		displayName = filepath.Base(pick.Path)
	}
	if err != nil {
		return fail(string(contract.PauseInvalidInput), fmt.Sprintf("专利清单《%s》无法使用：%v。请检查后重新上传。", displayName, err),
			map[string]any{"file": displayName}), nil
	}
	if len(rows) == 0 {
		return fail(string(contract.PauseInvalidInput), fmt.Sprintf("专利清单《%s》里没有任何专利编号。请重新上传。", displayName),
			map[string]any{"file": displayName}), nil
	}
	if _, err := LoadLibrary(LibraryDir(in.EmployeeDir)); err != nil {
		return ValidateOutput{}, err
	}
	return ValidateOutput{OK: true, List: &ListFile{Path: pick.Path, Name: displayName, Rows: len(rows)}}, nil
}
