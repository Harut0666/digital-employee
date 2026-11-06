// 员工配置加载与员工列表。
//
// 加载器统一用 internal/config（engine 负责，与引擎、CLI 共用同一份实现），
// 服务端只依赖 contract.EmployeeConfig；测试可通过 Options.LoadEmployee 注入替身。
package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"digital-employee/internal/config"
	"digital-employee/internal/contract"
)

// IsEmployeeID 员工 id 即目录名：只允许小写字母、数字和连字符。
func IsEmployeeID(id string) bool { return config.IsEmployeeID(id) }

// LoadEmployeeConfig 加载一个员工目录的完整配置（失败时返回中文说明）。
func LoadEmployeeConfig(dir string) (*contract.EmployeeConfig, error) {
	return config.LoadEmployeeConfig(dir)
}

// employeeInfo GET /api/employees 的列表项。
type employeeInfo struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	FormSchema     map[string]any         `json:"formSchema"`
	FormUI         map[string]any         `json:"formUi,omitempty"`
	ReviewSections []contract.SectionSpec `json:"reviewSections,omitempty"`
}

// listEmployees 列出含 workflow.yaml 的合法员工目录（按名称排序）。
func (s *Server) listEmployees() ([]employeeInfo, error) {
	entries, err := os.ReadDir(s.opts.EmployeesDir)
	if err != nil {
		return []employeeInfo{}, nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || !IsEmployeeID(name) {
			continue
		}
		if !isFile(filepath.Join(s.opts.EmployeesDir, name, "workflow.yaml")) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]employeeInfo, 0, len(names))
	for _, name := range names {
		loaded, err := s.opts.LoadEmployee(filepath.Join(s.opts.EmployeesDir, name))
		if err != nil {
			return nil, err
		}
		info := employeeInfo{
			ID:          loaded.ID,
			Name:        loaded.Workflow.Name,
			Description: loaded.Workflow.Description,
			FormSchema:  loaded.FormSchema,
			FormUI:      loaded.FormUI,
		}
		if info.FormSchema == nil {
			info.FormSchema = map[string]any{}
		}
		info.ReviewSections = loaded.QC.Builtin.Structure.Sections
		result = append(result, info)
	}
	return result, nil
}

func (s *Server) handleListEmployees(w http.ResponseWriter, r *http.Request) {
	employees, err := s.listEmployees()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, employees)
}
