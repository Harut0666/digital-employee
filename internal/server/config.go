// 员工配置页后端（T11）：配置读写、校验、版本与回滚（docs/design.md §4.1）。
// 对应 packages/server/src/routes/config.ts。
//
// 配置文件是唯一的数据来源：GET 返回每个文件的原文与解析结果，PUT 先校验再写回；
// 每次保存前把当前配置复制到 <workspace>/config-versions/<员工>/<时间戳>-<哈希短码>/。
// 保存写盘后整体加载失败时回滚文件并删掉那个版本，保证“校验失败不写任何文件”。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// loadedConfigView 配置页要读的加载结果（程序加载成功时才有）。
type loadedConfigView struct {
	Skill         string                    `json:"skill"`
	Frontdesk     string                    `json:"frontdesk"`
	Workflow      contract.EmployeeWorkflow `json:"workflow"`
	QC            contract.QCConfig         `json:"qc"`
	FormSchema    map[string]any            `json:"formSchema"`
	FormUI        map[string]any            `json:"formUi,omitempty"`
	Prompts       map[string]string         `json:"prompts"`
	OutputSchemas map[string]map[string]any `json:"outputSchemas"`
}

// formParamView 表单字段可绑定的程序参数。
type formParamView struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// employeeConfigViews 配置页需要的派生视图。
type employeeConfigViews struct {
	Skill       skillSections   `json:"skill"`
	Frontdesk   frontdeskDoc    `json:"frontdesk"`
	FormParams  []formParamView `json:"formParams"`
	QCTemplates []string        `json:"qcTemplates"`
}

// employeeConfigResponse GET /api/employees/:id/config 的响应。
type employeeConfigResponse struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Dir              string              `json:"dir"`
	Hash             string              `json:"hash,omitempty"`
	Files            []configFileInfo    `json:"files"`
	Config           *loadedConfigView   `json:"config,omitempty"`
	Views            employeeConfigViews `json:"views"`
	FormSchemaErrors []string            `json:"formSchemaErrors"`
	Versions         []configVersion     `json:"versions"`
	Error            string              `json:"error,omitempty"`
}

// saveConfigErrorBody 保存配置失败的响应（带逐条校验错误）。
type saveConfigErrorBody struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error"`
	Message string   `json:"message"`
	Errors  []string `json:"errors"`
}

// restoreConfigResponse 回滚响应。
type restoreConfigResponse struct {
	OK           bool            `json:"ok"`
	Version      configVersion   `json:"version"`
	RestoredFrom string          `json:"restoredFrom"`
	Versions     []configVersion `json:"versions"`
	Hash         string          `json:"hash,omitempty"`
}

func (s *Server) employeeDir(id string) (string, bool) {
	if !IsEmployeeID(id) {
		return "", false
	}
	dir := filepath.Join(s.opts.EmployeesDir, id)
	if !isFile(filepath.Join(dir, "workflow.yaml")) {
		return "", false
	}
	return dir, true
}

// lookupEmployee 员工 id / 目录的公共校验：不合法 400，不存在 404。
func (s *Server) lookupEmployee(w http.ResponseWriter, id string) (string, bool) {
	if !IsEmployeeID(id) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "员工 id 不合法：只允许小写字母、数字和连字符"))
		return "", false
	}
	dir, ok := s.employeeDir(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("员工不存在：%s", id)))
		return "", false
	}
	return dir, true
}

// employeeConfigPayload 组装配置页需要的全部内容；加载失败时仍返回文件原文，方便用“查看源文件”修。
func (s *Server) employeeConfigPayload(id, dir string) employeeConfigResponse {
	files := describeConfigFiles(dir)
	skillText := ""
	frontdeskText := ""
	formSchemaParsed := map[string]any{}
	for _, file := range files {
		switch file.Path {
		case "SKILL.md":
			skillText = file.Content
		case "frontdesk.md":
			frontdeskText = file.Content
		case "form.schema.json":
			if parsed, ok := file.Parsed.(map[string]any); ok {
				formSchemaParsed = parsed
			}
		}
	}

	response := employeeConfigResponse{
		ID:    id,
		Dir:   dir,
		Files: files,
		Views: employeeConfigViews{
			Skill:       splitSkill(skillText),
			Frontdesk:   parseFrontdesk(frontdeskText),
			FormParams:  formParamsView(),
			QCTemplates: append([]string{}, qcTemplates...),
		},
		Versions: listConfigVersions(s.opts.WorkspaceDir, id),
	}
	response.Name = id
	if config, err := s.opts.LoadEmployee(dir); err != nil {
		response.Error = "员工配置无法加载：" + err.Error()
		response.FormSchemaErrors = validateFormSchemaPurposes(formSchemaParsed)
	} else {
		response.Name = config.Workflow.Name
		response.Hash = config.Hash
		response.Config = &loadedConfigView{
			Skill: config.Skill, Frontdesk: config.Frontdesk,
			Workflow: config.Workflow, QC: config.QC,
			FormSchema: config.FormSchema, FormUI: config.FormUI,
			Prompts: config.Prompts, OutputSchemas: config.OutputSchemas,
		}
		schema := config.FormSchema
		if schema == nil {
			schema = formSchemaParsed
		}
		response.FormSchemaErrors = validateFormSchemaPurposes(schema)
	}
	return response
}

func formParamsView() []formParamView {
	out := make([]formParamView, 0, len(formParamNames))
	for _, name := range formParamNames {
		out = append(out, formParamView{Name: name, Label: contract.FormParams[name]})
	}
	return out
}

func (s *Server) handleGetEmployeeConfig(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.lookupEmployee(w, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.employeeConfigPayload(r.PathValue("id"), dir))
}

func (s *Server) handleListConfigVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.lookupEmployee(w, id); !ok {
		return
	}
	writeJSON(w, http.StatusOK, listConfigVersions(s.opts.WorkspaceDir, id))
}

func (s *Server) handleDiffConfigVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir, ok := s.lookupEmployee(w, id)
	if !ok {
		return
	}
	versionID := r.PathValue("version")
	diff, found := diffConfigVersion(s.opts.WorkspaceDir, id, dir, versionID)
	if !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("配置版本不存在：%s", versionID)))
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) handleRestoreConfigVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir, ok := s.lookupEmployee(w, id)
	if !ok {
		return
	}
	versionID := r.PathValue("version")
	result, found := restoreConfigVersion(s.opts.WorkspaceDir, id, dir, versionID, s.now)
	if !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("配置版本不存在：%s", versionID)))
		return
	}
	response := restoreConfigResponse{
		OK: true, Version: result.Snapshot, RestoredFrom: result.RestoredFrom,
		Versions: listConfigVersions(s.opts.WorkspaceDir, id),
	}
	if config, err := s.opts.LoadEmployee(dir); err == nil {
		response.Hash = config.Hash
	}
	writeJSON(w, http.StatusOK, response)
}

func writeSaveConfigError(w http.ResponseWriter, status int, message string, errors []string) {
	writeJSON(w, status, saveConfigErrorBody{
		OK: false, Error: contract.ErrInvalidInput, Message: message, Errors: errors,
	})
}

func (s *Server) handleSaveConfigFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir, ok := s.lookupEmployee(w, id)
	if !ok {
		return
	}
	rawPath := r.PathValue("path")
	if decoded, err := url.PathUnescape(rawPath); err == nil {
		rawPath = decoded
	}
	relPath, pathOK := normalizeConfigPath(rawPath)
	if !pathOK {
		message := fmt.Sprintf("不允许写入配置文件：%s", rawPath)
		writeSaveConfigError(w, http.StatusBadRequest, message, []string{message})
		return
	}

	body, err := readBodyJSON(r)
	if err != nil {
		writeSaveConfigError(w, http.StatusBadRequest, err.Error(), []string{err.Error()})
		return
	}
	input := configFileInput{}
	if rawContent, present := body["content"]; present {
		text, ok := rawContent.(string)
		if !ok {
			writeSaveConfigError(w, http.StatusBadRequest, "content 必须是字符串", []string{"content 必须是字符串"})
			return
		}
		input.Content = &text
	}
	if rawData, present := body["data"]; present {
		input.Data = rawData
		input.HasData = true
	}
	_, currentText, currentOK := readConfigFileText(dir, relPath)
	var currentPtr *string
	if currentOK {
		currentPtr = &currentText
	}
	if _, formText, ok := readConfigFileText(dir, "form.schema.json"); ok {
		_ = json.Unmarshal([]byte(formText), &input.FormSchema)
	}
	prepared, errors := prepareConfigText(relPath, input, currentPtr)
	if len(errors) > 0 {
		writeSaveConfigError(w, http.StatusBadRequest, "配置校验未通过", errors)
		return
	}

	// 保存前能加载的配置，保存后也必须能加载：写盘后整体加载校验，失败就回滚文件并删掉快照
	loadedBefore := true
	if _, err := s.opts.LoadEmployee(dir); err != nil {
		loadedBefore = false
	}
	version, err := createConfigVersion(s.opts.WorkspaceDir, id, dir, s.now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	target := filepath.Join(dir, filepath.FromSlash(relPath))
	var backup *string
	if currentOK {
		backup = &currentText
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	if err := os.WriteFile(target, []byte(prepared), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}

	hash := ""
	loadError := ""
	if config, err := s.opts.LoadEmployee(dir); err != nil {
		loadError = err.Error()
	} else {
		hash = config.Hash
	}
	if loadedBefore && loadError != "" {
		if backup == nil {
			_ = os.Remove(target)
		} else {
			_ = os.WriteFile(target, []byte(*backup), 0o644)
		}
		removeConfigVersion(s.opts.WorkspaceDir, id, version.ID)
		writeSaveConfigError(w, http.StatusBadRequest, "配置保存后无法加载，已回滚", []string{loadError})
		return
	}
	response := map[string]any{"ok": true, "path": relPath, "version": version}
	if hash != "" {
		response["hash"] = hash
	}
	writeJSON(w, http.StatusOK, response)
}

var _ = strings.TrimSpace
