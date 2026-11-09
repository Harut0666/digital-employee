// read_file 的读取与分页（带行号的文本）。
//
// 参考 DeepSeek Harness 的 read 工具的行为，不复制代码：offset 从 1 起，单次最多 2000 行，每行最多 2000 个字符，
// 选中的内容最多 50KB；二进制（前 8192 字节含 NUL）和非法 UTF-8 报“不是文本文件”；
// 整个文件按流读取，所以大文件也能统计总行数，内存只留当前窗口。
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"digital-employee/internal/contract"
)

const (
	// readMaxLines 单次最多读多少行。
	readMaxLines = 2000
	// readMaxLineChars 每行最多显示多少个字符。
	readMaxLineChars = 2000
	// readMaxBytes 选中的内容最多多少字节。
	readMaxBytes = 50 * 1024
	// readBinaryProbe 判断二进制时看文件开头多少字节。
	readBinaryProbe = 8192
	// readKeepLineBytes 一行最多留多少字节在内存里（2000 个字符最多 8000 字节）。
	readKeepLineBytes = readMaxLineChars * 4
)

// readOutcome 一次读取的结果。
type readOutcome struct {
	// Text 带行号的正文，末尾是页脚
	Text string
	// Total 文件总行数；Start、End 是这次显示的第一行和最后一行（空文件为 0）
	Total, Start, End int
	// Truncated 没读到文件末尾（行数、字节数到了上限，或请求的窗口之后还有内容）
	Truncated bool
}

var errNotText = errors.New("不是文本文件（含有二进制数据或不是合法的 UTF-8），不能用 read_file 读")

// utf8Stream 跨块检查 UTF-8：块的末尾可能切在一个字符中间，多出来的字节留到下一块。
type utf8Stream struct{ carry []byte }

func (s *utf8Stream) feed(chunk []byte) bool {
	data := chunk
	if len(s.carry) > 0 {
		data = append(append([]byte{}, s.carry...), chunk...)
	}
	cut := len(data)
	for back := 1; back <= 3 && back <= len(data); back++ {
		if !utf8.RuneStart(data[len(data)-back]) {
			continue
		}
		if !utf8.FullRune(data[len(data)-back:]) {
			cut = len(data) - back
		}
		break
	}
	s.carry = append(s.carry[:0], data[cut:]...)
	return utf8.Valid(data[:cut])
}

// finish 行结束：没有留着半个字符才合法。
func (s *utf8Stream) finish() bool {
	ok := len(s.carry) == 0
	s.carry = s.carry[:0]
	return ok
}

// readLines 从 r 里读第 offset 行起最多 limit 行（limit<=0 取上限）。offset 从 1 起。
func readLines(r io.Reader, offset, limit int) (readOutcome, error) {
	if offset < 1 {
		return readOutcome{}, fmt.Errorf("offset 从 1 起（第一行是 1），收到 %d", offset)
	}
	if limit <= 0 || limit > readMaxLines {
		limit = readMaxLines
	}
	reader := bufio.NewReaderSize(r, 64*1024)
	var (
		selected     []string
		selectedSize int
		byteCapped   bool
		lineNo       int
		kept         []byte
		overflow     bool
		started      bool
		valid        utf8Stream
	)
	emit := func() {
		lineNo++
		text := kept
		if lineNo == 1 {
			text = bytes.TrimPrefix(text, []byte{0xEF, 0xBB, 0xBF})
		}
		in := lineNo >= offset && lineNo < offset+limit
		if in && !byteCapped {
			shown := string(text)
			if runes := []rune(shown); len(runes) > readMaxLineChars {
				shown = string(runes[:readMaxLineChars]) + "…（该行已截断，只显示前 2000 个字符）"
			} else if overflow {
				shown += "…（该行已截断，只显示前 2000 个字符）"
			}
			line := fmt.Sprintf("%d: %s", lineNo, shown)
			if len(selected) > 0 && selectedSize+len(line)+1 > readMaxBytes {
				byteCapped = true
			} else {
				selected = append(selected, line)
				selectedSize += len(line) + 1
			}
		}
		kept, overflow, started = kept[:0], false, false
	}
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(chunk) > 0 {
			started = true
			if bytes.IndexByte(chunk, 0) >= 0 {
				return readOutcome{}, errNotText
			}
			if !valid.feed(chunk) {
				return readOutcome{}, errNotText
			}
			body := bytes.TrimRight(chunk, "\r\n")
			if room := readKeepLineBytes - len(kept); room > 0 {
				if len(body) > room {
					kept = append(kept, body[:room]...)
					overflow = true
				} else {
					kept = append(kept, body...)
				}
			} else if len(body) > 0 {
				overflow = true
			}
		}
		switch {
		case err == nil:
			if !valid.finish() {
				return readOutcome{}, errNotText
			}
			emit()
		case errors.Is(err, bufio.ErrBufferFull):
			// 一行还没读完，接着读
		case errors.Is(err, io.EOF):
			if started {
				if !valid.finish() {
					return readOutcome{}, errNotText
				}
				emit()
			}
			return finishRead(selected, offset, lineNo, byteCapped)
		default:
			return readOutcome{}, err
		}
	}
}

func finishRead(selected []string, offset, total int, byteCapped bool) (readOutcome, error) {
	if total == 0 {
		return readOutcome{Text: "（文件是空的，共 0 行）"}, nil
	}
	if offset > total {
		return readOutcome{}, fmt.Errorf("offset 超出范围：文件共 %d 行，offset 应在 1–%d 之间", total, total)
	}
	end := offset + len(selected) - 1
	var footer string
	switch {
	case byteCapped:
		footer = fmt.Sprintf("（输出已截断：超过 50KB，显示第 %d–%d 行，共 %d 行，用 offset=%d 继续）", offset, end, total, end+1)
	case end < total:
		footer = fmt.Sprintf("（显示第 %d–%d 行，共 %d 行，用 offset=%d 继续）", offset, end, total, end+1)
	default:
		footer = fmt.Sprintf("（已到文件末尾，共 %d 行）", total)
	}
	return readOutcome{
		Text:  strings.Join(selected, "\n") + "\n\n" + footer,
		Total: total, Start: offset, End: end, Truncated: end < total,
	}, nil
}

// readTextFile 读磁盘上的文本文件：先看开头有没有 NUL，再按流分页。
func readTextFile(path string, offset, limit int) (readOutcome, error) {
	file, err := os.Open(path)
	if err != nil {
		return readOutcome{}, err
	}
	defer file.Close()
	head := make([]byte, readBinaryProbe)
	count, readErr := io.ReadFull(file, head)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return readOutcome{}, readErr
	}
	if bytes.IndexByte(head[:count], 0) >= 0 {
		return readOutcome{}, errNotText
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return readOutcome{}, err
	}
	return readLines(file, offset, limit)
}

// wholeNumber 模型给的数字（JSON 里是浮点）是不是整数。
func wholeNumber(raw any) (int, bool) {
	switch value := raw.(type) {
	case float64:
		if value == float64(int(value)) {
			return int(value), true
		}
	case int:
		return value, true
	case int64:
		return int(value), true
	}
	return 0, false
}

// parsedText 用现有的文件解析（xlsx、docx）把一个文件读成文本。
func (t *turn) parsedText(target string) (string, error) {
	client := t.manager.deps.Tools
	if client == nil {
		return "", errors.New("没有可用的工具客户端")
	}
	result, err := client.Call(contract.ToolReadFile, map[string]any{"path": target})
	if err != nil {
		return "", err
	}
	if !result.OK {
		return "", errors.New(result.Error)
	}
	switch data := result.Data.(type) {
	case contract.ReadFileOutput:
		return data.Text, nil
	case map[string]any:
		text, _ := data["text"].(string)
		return text, nil
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		return "", err
	}
	var out contract.ReadFileOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}
