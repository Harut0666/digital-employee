// 目录材料（T41）：前台 Agent 读工作区目录里的文件，经人确认后把副本作为材料。
//
// 工作区只是一个本机目录。前台 Agent 能列目录、读文本，但只能碰工作区目录里面：
// 路径先解析（绝对路径、..、符号链接都会解开）再判断是否仍在目录内，越界就拒绝并写进会话日志。
// 人确认后程序把文件复制进会话材料，之后和上传的材料一样用；原文件再改也不影响已开工的任务。
package session

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// listFilesLimit list_files 和 @ 选择最多列出多少项。
const listFilesLimit = 200

// FolderEntry 工作区目录里的一项；路径相对工作区目录，统一用 / 分隔。
type FolderEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Dir       bool   `json:"dir,omitempty"`
	Size      int64  `json:"size,omitempty"`
	ModTime   int64  `json:"modTime,omitempty"`
	Extension string `json:"ext,omitempty"`
}

// FolderListing 一次列目录的结果。
type FolderListing struct {
	Entries   []FolderEntry `json:"entries"`
	Truncated bool          `json:"truncated"`
}

// workspaceHidden 前台 Agent 列目录时不列的子文件夹：程序自己写进去的，“交付”另说。
var workspaceHidden = map[string]bool{"材料": true}

// atHidden @ 选择时不列的子文件夹：程序自己写出来的东西不是人的工作文件。
var atHidden = map[string]bool{"材料": true, "交付": true}

// relativeSegments 把路径拆成一段一段（\ 和 / 都认）。绝对路径立刻拒绝。
func relativeSegments(raw string) ([]string, error) {
	clean := strings.TrimSpace(strings.ReplaceAll(raw, `\`, "/"))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "/") || filepath.VolumeName(clean) != "" {
		return nil, fmt.Errorf("不能用绝对路径：%s", raw)
	}
	segments := []string{}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." {
			continue
		}
		segments = append(segments, part)
	}
	return segments, nil
}

// withinRoot target 是 root 本身或 root 下的一项。Windows 上大小写不敏感。
func withinRoot(root, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	separator := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		rootAbs = strings.ToLower(rootAbs)
		targetAbs = strings.ToLower(targetAbs)
	}
	if targetAbs == rootAbs {
		return true
	}
	return strings.HasPrefix(targetAbs, strings.TrimSuffix(rootAbs, separator)+separator)
}

// resolveInWorkspace 把用户给的路径解析成工作区目录里的真实路径。
// 不是绝对路径、逐段解析（链接先解开）后仍在根目录内，否则返回错误。
func resolveInWorkspace(root, raw string) (string, string, error) {
	segments, err := relativeSegments(raw)
	if err != nil {
		return "", "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	current := rootAbs
	for _, part := range segments {
		if part == ".." {
			current = filepath.Dir(current)
		} else {
			current = filepath.Join(current, part)
		}
		if resolved, evalErr := filepath.EvalSymlinks(current); evalErr == nil {
			current = resolved
		} else if !errors.Is(evalErr, fs.ErrNotExist) {
			return "", "", evalErr
		}
		if !withinRoot(rootAbs, current) {
			return "", "", fmt.Errorf("路径在工作区目录之外：%s", raw)
		}
	}
	rel, err := filepath.Rel(rootAbs, current)
	if err != nil {
		return "", "", err
	}
	return current, filepath.ToSlash(rel), nil
}

// hiddenName 点在开头的名字（隐藏文件）。
func hiddenName(name string) bool {
	return strings.HasPrefix(name, ".")
}

// listFolder 列出工作区目录（或其子目录）里的文件和文件夹，跳过隐藏文件和 exclude 里的子目录，最多 200 项。
func listFolder(base, sub string, exclude map[string]bool) (FolderListing, error) {
	root, err := filepath.Abs(base)
	if err != nil {
		return FolderListing{}, err
	}
	dir, rel, err := resolveInWorkspace(root, sub)
	if err != nil {
		return FolderListing{}, denied("%s。我只能看工作区目录里的东西。", err.Error())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return FolderListing{}, fmt.Errorf("目录不存在：%s", displayPath(rel))
	}
	if !dirInfo.IsDir() {
		return FolderListing{}, fmt.Errorf("不是目录：%s", displayPath(rel))
	}
	children, err := os.ReadDir(dir)
	if err != nil {
		return FolderListing{}, fmt.Errorf("读不了目录 %s：%v", displayPath(rel), err)
	}
	prefix := ""
	if rel != "." {
		prefix = rel + "/"
	}
	entries := make([]FolderEntry, 0, len(children))
	for _, child := range children {
		name := child.Name()
		if hiddenName(name) || exclude[name] {
			continue
		}
		path := filepath.Join(dir, name)
		// 链接看它指向哪：指到目录外的既不列，也不能当材料
		if child.Type()&fs.ModeSymlink != 0 {
			resolved, linkErr := filepath.EvalSymlinks(path)
			if linkErr != nil || !withinRoot(root, resolved) {
				continue
			}
		}
		// 每一项都按真实文件系统判断（链接解到目录外的会在这里落空）
		fileInfo, statErr := os.Lstat(path)
		if statErr != nil {
			continue
		}
		if fileInfo.Mode()&fs.ModeSymlink != 0 {
			if fileInfo, statErr = os.Stat(path); statErr != nil {
				continue
			}
		}
		if fileInfo.IsDir() {
			entries = append(entries, FolderEntry{Path: prefix + name, Name: name, Dir: true})
			continue
		}
		entries = append(entries, FolderEntry{
			Path: prefix + name, Name: name,
			Size: fileInfo.Size(), ModTime: fileInfo.ModTime().UnixNano(),
			Extension: strings.ToLower(filepath.Ext(name)),
		})
	}
	// 文件夹在前，同类按名字排；超过上限截断
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].Name < entries[j].Name
	})
	listing := FolderListing{Entries: entries}
	if len(entries) > listFilesLimit {
		listing.Entries = entries[:listFilesLimit]
		listing.Truncated = true
	}
	return listing, nil
}

func displayPath(rel string) string {
	if rel == "." {
		return "工作区目录"
	}
	return rel
}

// FolderFiles 人可以在 @ 里选的目录文件（不列“交付”和“材料”）。
func (m *Manager) FolderFiles(id string) (FolderListing, error) {
	current, err := m.load(id)
	if err != nil {
		return FolderListing{}, err
	}
	root := m.workspaceDir(current.workspaceID)
	if root == "" {
		return FolderListing{}, failure(ErrWorkspaceUnknown, "工作区目录不存在：%s", current.workspaceID)
	}
	listing, err := listFolder(root, ".", atHidden)
	var refused *denial
	if errors.As(err, &refused) {
		return FolderListing{}, failure(contract.ErrInvalidInput, "%s", refused.reason)
	}
	return listing, err
}

// folderTools 员工接手后总会开放的目录工具：只读，路径越界由 call 接口拒绝并留痕。
func (t *turn) folderTools() []tool {
	list := tool{
		spec: agent.ToolSpec{
			Name:        "list_files",
			Description: "列出工作区目录里的文件和文件夹，可以指定子目录（如“资料”）。跳过隐藏文件，最多列出 200 项。",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"dir": map[string]any{"type": "string", "description": "子目录，相对工作区目录；不填就是工作区目录本身"},
			}},
		},
		run: func(arguments map[string]any) (any, error) {
			sub, _ := arguments["dir"].(string)
			return listFolder(t.workspaceDir(), sub, workspaceHidden)
		},
	}
	read := tool{
		spec: agent.ToolSpec{
			Name:        "read_file",
			Description: "读工作区目录里一个文件的文本内容，支持 csv、xlsx、docx、txt、md，超过 2 万字截断。",
			Parameters: map[string]any{"type": "object", "required": []any{"path"}, "properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "文件路径，相对工作区目录"},
			}},
		},
		run: func(arguments map[string]any) (any, error) {
			requested, _ := arguments["path"].(string)
			target, _, err := resolveInWorkspace(t.workspaceDir(), requested)
			if err != nil {
				return nil, denied("%s。我只能读工作区目录里的文件。", err.Error())
			}
			client := t.manager.deps.Tools
			if client == nil {
				return nil, errors.New("没有可用的工具客户端")
			}
			result, err := client.Call(contract.ToolReadFile, map[string]any{"path": target})
			if err != nil {
				return nil, err
			}
			if !result.OK {
				return nil, errors.New(result.Error)
			}
			return result.Data, nil
		},
	}
	use := tool{
		spec: agent.ToolSpec{
			Name:        "use_folder_file",
			Description: "提议把工作区目录里的一个文件用作本次任务的材料。程序会发出“使用目录文件”卡片，由人确认；人确认后文件才复制进会话材料。",
			Parameters: map[string]any{"type": "object", "required": []any{"path"}, "properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "文件路径，相对工作区目录"},
				"reason": map[string]any{"type": "string", "description": "一句话说明为什么用这个文件"},
			}},
		},
		run: func(arguments map[string]any) (any, error) {
			requested, _ := arguments["path"].(string)
			reason, _ := arguments["reason"].(string)
			target, rel, err := resolveInWorkspace(t.workspaceDir(), requested)
			if err != nil {
				return nil, denied("%s。我只能用工作区目录里的文件。", err.Error())
			}
			info, err := os.Stat(target)
			if err != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("没有这个文件：%s", rel)
			}
			t.cards = append(t.cards, Card{
				Type: CardUseFile, Path: rel, Name: filepath.Base(target), Size: info.Size(), Reason: reason,
			})
			return map[string]any{"proposed": rel}, nil
		},
	}
	return []tool{list, read, use}
}

// workspaceDir 会话所在工作区的目录；工作区目录不存在时返回空串。
func (t *turn) workspaceDir() string {
	return t.manager.workspaceDir(t.current.workspaceID)
}

func (m *Manager) workspaceDir(workspaceID string) string {
	if m.deps.WorkspaceDir == nil {
		return ""
	}
	return m.deps.WorkspaceDir(workspaceID)
}

// folderPathProblem 人确认或 @ 选中的路径能不能当材料：在工作区目录里、不是程序自己的文件夹或隐藏文件。
func folderPathProblem(root, requested string) (string, string, error) {
	target, rel, err := resolveInWorkspace(root, requested)
	if err != nil {
		return "", "", failure(contract.ErrInvalidInput, "这个路径不在工作区目录里：%s", requested)
	}
	if rel == "." {
		return "", "", failure(contract.ErrInvalidInput, "这是工作区目录本身，不是文件")
	}
	for _, segment := range strings.Split(rel, "/") {
		if hiddenName(segment) || atHidden[segment] {
			return "", "", failure(contract.ErrInvalidInput, "不能用程序自己的文件夹或隐藏文件：%s", rel)
		}
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", failure(contract.ErrInvalidInput, "没有这个文件：%s", rel)
	}
	return target, rel, nil
}

// folderConfirmedNotice 人确认目录文件后，写进会话的那句话；读取时按它把这些卡片标成已确认。
func folderConfirmedNotice(rels []string) string {
	return "已把目录里的文件作为材料：" + strings.Join(rels, "、")
}

// UseFolderFiles 人在“使用目录文件”卡片上确认（@ 选择走同一个入口）：把目录里的文件复制进会话材料，
// 之后与上传的材料等效。一批里有一个路径不能用就整批拒绝，不留半份材料。
func (m *Manager) UseFolderFiles(id string, role contract.Role, paths []string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以确认目录里的文件")
	}
	if len(paths) == 0 {
		return nil, failure(contract.ErrInvalidInput, "没有选择文件")
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if current.taskID != "" {
		return nil, failure(ErrSessionHasTask, "任务已经开始，请在暂停卡片上补充材料")
	}
	root := m.workspaceDir(current.workspaceID)
	if root == "" {
		return nil, failure(ErrWorkspaceUnknown, "工作区目录不存在：%s", current.workspaceID)
	}
	// 先全部校验，再把副本写进会话
	type confirmed struct{ target, rel string }
	batch := make([]confirmed, 0, len(paths))
	seen := map[string]bool{}
	for _, requested := range paths {
		target, rel, err := folderPathProblem(root, requested)
		if err != nil {
			return nil, err
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		batch = append(batch, confirmed{target: target, rel: rel})
	}
	t := &turn{manager: m, current: current}
	// 确认过的材料写进会话日志（attachment_added），人和程序都看得到这份材料
	names := make([]string, 0, len(batch))
	for _, item := range batch {
		size, err := m.store.saveAttachment(id, contract.AttachmentInput{Name: filepath.Base(item.target), SourcePath: item.target})
		if err != nil {
			return nil, err
		}
		if err := t.write(Message{Kind: kindAttachmentAdded, Attachment: &Attachment{Name: filepath.Base(item.target), Size: size}}); err != nil {
			return nil, err
		}
		names = append(names, item.rel)
	}
	// 再说一句“已把……”：这就是人确认这几张卡片的记录，读取时据此把卡片标成已确认
	if err := t.write(Message{Kind: KindUser, Role: contract.RoleInitiator, Text: folderConfirmedNotice(names)}); err != nil {
		return nil, err
	}
	return m.reply(t)
}

// SaveWorkspaceMaterials 对话里上传的文件同时写一份到 <目录>/材料/，重名加序号、不覆盖。
// 写不进目录不影响这次上传（材料已经暂存在会话里），只在调用方记一条说明。
func (m *Manager) SaveWorkspaceMaterials(workspaceID string, files []contract.AttachmentInput) error {
	root := m.workspaceDir(workspaceID)
	if root == "" {
		return fmt.Errorf("工作区目录不存在：%s", workspaceID)
	}
	failures := []string{}
	for _, file := range files {
		if err := copyIntoMaterials(root, file); err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", file.Name, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "；"))
	}
	return nil
}

// copyIntoMaterials 复制一份到 <目录>/材料/，重名时依次试“名字(2)”“名字(3)”……
func copyIntoMaterials(root string, file contract.AttachmentInput) error {
	dir := filepath.Join(root, "材料")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	source, err := os.Open(file.SourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	base := filepath.Base(file.Name)
	extension := filepath.Ext(base)
	stem := strings.TrimSuffix(base, extension)
	target := ""
	var out *os.File
	for index := 1; index <= 1000 && out == nil; index++ {
		name := base
		if index > 1 {
			name = fmt.Sprintf("%s(%d)%s", stem, index, extension)
		}
		candidate := filepath.Join(dir, name)
		// O_EXCL：并发时同名只成功一个，失败的那个换下一个序号
		created, createErr := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(createErr, fs.ErrExist) {
			continue
		}
		if createErr != nil {
			return createErr
		}
		target, out = candidate, created
	}
	if out == nil {
		return errors.New("同名文件太多，无法写入“材料”")
	}
	if _, copyErr := io.Copy(out, source); copyErr != nil {
		out.Close()
		_ = os.Remove(target)
		return copyErr
	}
	return out.Close()
}
