// 会话事件流的桥：订阅某个会话之后的事件，交给 HTTP 层写成 SSE。
//
// 与 Manager 分开一个文件，两个并行改会话模块的工单各占各的文件，合并时冲突少。

package session

// EventSource 会话事件的来源（Manager 实现它）。
type EventSource interface {
	// Activity 会话现在有没有一轮在跑，以及这一轮已经输出的内容
	Activity(id string) (*AgentActivity, error)
	// Subscribe 订阅之后的事件；返回取消订阅的函数
	Subscribe(id string, notify func([]AgentEvent)) (func(), error)
}
