// 端口选择（对应 packages/server/src/port.ts）：
// 默认 8787，被占用时往下一个端口试（设计 §14：控制台打印实际地址）。
package app

import (
	"fmt"
	"net"
)

// ChoosePort 从 start 开始找第一个空闲端口；都不可用时报错（错误里带上试过的区间）。
func ChoosePort(start, attempts int, isAvailable func(int) bool) (int, error) {
	if attempts < 1 {
		attempts = 20
	}
	for index := 0; index < attempts; index++ {
		port := start + index
		if isAvailable(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("端口 %d-%d 都被占用，请用 DE_PORT 指定一个空闲端口", start, start+attempts-1)
}

// IsPortAvailable 试听一个端口：能 Bind 就立刻释放并返回 true。
func IsPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
