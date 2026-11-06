// 请求解析与错误映射：角色头、上传附件落盘、引擎错误码到 HTTP 状态码。
// 与 packages/server/src/http.ts 一一对应。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

// requestRoles 可以由 HTTP 头声明的角色（system 只出现在事件里）。
var requestRoles = []contract.Role{contract.RoleInitiator, contract.RoleReviewer, contract.RoleOperator}

// apiErrorBody 错误响应体：{ok:false, error, message}。
type apiErrorBody struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func apiError(code, message string) apiErrorBody {
	return apiErrorBody{OK: false, Error: code, Message: message}
}

// 业务错误码 → HTTP 状态码（与 http.ts 的 ERROR_STATUS 一致）。
var errorStatus = map[string]int{
	contract.ErrInvalidInput:      http.StatusBadRequest,
	contract.ErrForbiddenRole:     http.StatusForbidden,
	contract.ErrNotFound:          http.StatusNotFound,
	contract.ErrIllegalTransition: http.StatusConflict,
}

func statusOf(code string) int {
	if status, ok := errorStatus[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// writeJSON 写 JSON 响应（紧凑格式，与 Elysia 一致）。
func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"ok":false,"error":"internal","message":"响应序列化失败"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeAPIError(w http.ResponseWriter, status int, body apiErrorBody) {
	writeJSON(w, status, body)
}

// internalErrorBody 500 响应体：{ok:false,error:"internal",message}。
type internalErrorBody struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func internalError(message string) internalErrorBody {
	return internalErrorBody{OK: false, Error: "internal", Message: message}
}

// roleOf 角色由 x-role 头给出；缺失或取值未知都视为越权，校验本身由引擎完成。
func roleOf(r *http.Request) (contract.Role, bool) {
	raw := r.Header.Get("x-role")
	for _, role := range requestRoles {
		if string(role) == raw {
			return role, true
		}
	}
	return "", false
}

var roleRequired = apiError("forbidden_role", "缺少或无法识别的角色（请求头 x-role 应为 initiator / reviewer / operator）")

var digitsPattern = regexp.MustCompile(`^\d+$`)

// parseAfter ?after= / Last-Event-ID：缺省或空串为 0；不是非负整数时返回 false。
func parseAfter(value string) (int64, bool) {
	if value == "" {
		return 0, true
	}
	if !digitsPattern.MatchString(value) {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseJSONField 表单字段既可能是 JSON 字符串，也可能已被解析成对象。
func parseJSONField(value any) (any, bool) {
	text, ok := value.(string)
	if !ok {
		return value, true
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

var commandTypes = []string{
	contract.CmdSubmit, contract.CmdSupplement, contract.CmdContinueWithoutMinutes,
	contract.CmdContinueWithPartial, contract.CmdRetry, contract.CmdTerminate,
	contract.CmdApprove, contract.CmdReturn,
}

func containsStringValue(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// parseCommand 只做形状检查；业务校验（角色、流转）全部交给引擎。
func parseCommand(raw any) (contract.TaskCommand, string) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return contract.TaskCommand{}, "命令必须是 JSON 对象"
	}
	typeValue, ok := obj["type"].(string)
	if !ok || !containsStringValue(commandTypes, typeValue) {
		return contract.TaskCommand{}, fmt.Sprintf("未知命令类型：%v", obj["type"])
	}
	if typeValue == contract.CmdReturn {
		comment, ok := obj["comment"].(string)
		if !ok || strings.TrimSpace(comment) == "" {
			return contract.TaskCommand{}, "退回必须填写意见 comment"
		}
		if rawSections, present := obj["sections"]; present {
			sections, ok := rawSections.([]any)
			if !ok {
				return contract.TaskCommand{}, "sections 必须是字符串数组"
			}
			for _, item := range sections {
				if _, ok := item.(string); !ok {
					return contract.TaskCommand{}, "sections 必须是字符串数组"
				}
			}
		}
	}
	if typeValue == contract.CmdSupplement {
		if rawForm, present := obj["form"]; present {
			if _, ok := rawForm.(map[string]any); !ok {
				return contract.TaskCommand{}, "form 必须是对象"
			}
		}
	}
	// 附件只能来自 multipart 上传（由服务端落盘）；JSON 里自带的 sourcePath 会读任意本地文件
	if _, present := obj["attachments"]; present {
		return contract.TaskCommand{}, "附件只能通过 multipart 上传，不接受命令 JSON 中的 attachments"
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return contract.TaskCommand{}, "命令不是合法的 JSON"
	}
	var command contract.TaskCommand
	if err := json.Unmarshal(data, &command); err != nil {
		return contract.TaskCommand{}, "命令不是合法的 JSON"
	}
	return command, ""
}

// savedUploads 上传文件落盘的临时结果。
type savedUploads struct {
	attachments []contract.AttachmentInput
	dir         string
}

// cleanup 引擎复制完附件后调用，清理临时目录。
func (u *savedUploads) cleanup() {
	if u.dir != "" {
		_ = os.RemoveAll(u.dir)
	}
}

// multipartFileHeader 上传文件句柄的别名，省去各文件的 import。
type multipartFileHeader = multipart.FileHeader

// saveUploads 把上传的文件写到临时目录，引擎会再把它们复制进任务目录。
// 文件名只保留最后一段，防止路径穿越。
func saveUploads(files []*multipart.FileHeader) (*savedUploads, error) {
	dir, err := os.MkdirTemp("", "de-upload-")
	if err != nil {
		return nil, err
	}
	result := &savedUploads{dir: dir, attachments: []contract.AttachmentInput{}}
	for index, header := range files {
		name := sanitizeUploadName(header.Filename, index)
		slot := filepath.Join(dir, strconv.Itoa(index))
		if err := os.MkdirAll(slot, 0o755); err != nil {
			result.cleanup()
			return nil, err
		}
		sourcePath := filepath.Join(slot, name)
		if err := copyUploadedFile(header, sourcePath); err != nil {
			result.cleanup()
			return nil, err
		}
		result.attachments = append(result.attachments, contract.AttachmentInput{Name: name, SourcePath: sourcePath})
	}
	return result, nil
}

func sanitizeUploadName(raw string, index int) string {
	normalized := strings.ReplaceAll(raw, "\\", "/")
	name := filepath.Base(normalized)
	if name == "" || name == "." || name == "/" || name == ".." {
		return fmt.Sprintf("file-%d", index)
	}
	return name
}

func copyUploadedFile(header *multipart.FileHeader, target string) error {
	source, err := header.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, source)
	return err
}

// readBodyJSON 读 JSON 请求体；空体返回空对象。
func readBodyJSON(r *http.Request) (map[string]any, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, errors.New("请求体不是合法的 JSON")
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("请求体必须是 JSON 对象")
	}
	return obj, nil
}

func isMultipart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}

// readMultipart 解析 multipart 表单（上传大小上限 256MB，内存 32MB）。
func readMultipart(r *http.Request) (*multipart.Form, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	if r.MultipartForm == nil {
		return &multipart.Form{Value: map[string][]string{}, File: map[string][]*multipart.FileHeader{}}, nil
	}
	return r.MultipartForm, nil
}

func formValue(form *multipart.Form, key string) (string, bool) {
	if form == nil {
		return "", false
	}
	values := form.Value[key]
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}
