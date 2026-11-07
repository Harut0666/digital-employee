// 入库确认：交付后模型起草一句描述，人在卡片上确认或修改后入库（ADR-0005）。
// 入库本身走知识库接口，这里只负责卡片和描述草稿；模型不可用时卡片照样可以手写。
package session

import (
	"context"
	"errors"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// FilingOffer 一个已交付任务能放进知识库的交付物。
type FilingOffer struct {
	// Title 默认标题（交付物的下载名）
	Title string
	// Filed 已入库的条目 ID；还没入库时为空
	Filed string
}

// FilingSource 入库卡片要用的知识库信息，由服务端接上。
type FilingSource interface {
	// Offer 任务有可入库的交付物时返回默认标题和入库情况
	Offer(taskID string) (FilingOffer, bool)
	// Text 交付物的正文，给模型起草描述
	Text(taskID string) (string, error)
}

// filingTextLimit 起草描述时交给模型的正文上限（字数）。描述只要一句话，前面的内容足够。
const filingTextLimit = 6000

const filingPrompt = `你在帮发起人把一份已交付的文件放进知识库。以后的员工先看描述，再决定要不要读正文，所以描述要让人一眼看出这份文件是什么、能回答什么问题。

- 只写一句话，不超过 60 个字：是什么文件、哪个时期、讲了哪些要点。
- 只依据下面的正文，不要编造。
- 直接输出这句描述，不要加引号、前缀或解释。`

const filingUnavailableText = "模型暂时不可用，没能起草描述。请在入库卡片上自己写一句描述。"

// DraftFiling 人在入库卡片上请模型起草描述。草稿写进会话日志，入库仍要人确认。
func (m *Manager) DraftFiling(ctx context.Context, id string, role contract.Role) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以把交付物放进知识库")
	}
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	notYet := failure(contract.ErrIllegalTransition, "任务交付之后才能放进知识库")
	if t.current.taskID == "" || m.deps.Filing == nil {
		return nil, notYet
	}
	snapshot, found := m.deps.Engine.Get(t.current.taskID)
	if !found || snapshot.State != contract.StateDelivered {
		return nil, notYet
	}
	if _, ok := m.deps.Filing.Offer(t.current.taskID); !ok {
		return nil, failure(contract.ErrNotFound, "这个任务没有可以放进知识库的交付物")
	}
	text, err := m.deps.Filing.Text(t.current.taskID)
	if err != nil {
		return nil, err
	}
	if runes := []rune(text); len(runes) > filingTextLimit {
		text = string(runes[:filingTextLimit])
	}

	description, err := t.converse(ctx, filingPrompt, []agent.Message{textMessage("user", text)}, nil)
	if err != nil && !errors.Is(err, errModelUnavailable) {
		return nil, err
	}
	if description == "" {
		if err := t.write(Message{Kind: kindFilingDrafted, Reason: "模型不可用"}); err != nil {
			return nil, err
		}
		if err := t.write(Message{Kind: KindNotice, Text: filingUnavailableText}); err != nil {
			return nil, err
		}
	} else if err := t.write(Message{Kind: kindFilingDrafted, TaskID: t.current.taskID, Text: description}); err != nil {
		return nil, err
	}
	return m.reply(t)
}
