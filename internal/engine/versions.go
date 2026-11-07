// 审核版本：每次送审产出一版交付物，由 deliverable_created 事件重放得到，不新增事件。
package engine

import (
	"encoding/json"
	"os"

	"digital-employee/internal/contract"
)

// versionRecord 一版交付物的投影。draftPath 是这一版写作步骤的输出，修改说明从里面读。
type versionRecord struct {
	reworkRound int
	createdAt   string
	files       []contract.DeliverableRef
	feedback    *contract.Feedback
	draftPath   string
	// notes 读过一次就缓存；notesRead 区分“读过但没有”和“还没读”
	notes     []contract.RevisionNote
	notesRead bool
}

// recordVersion 登记一个交付物文件：同一轮的文件归入同一版，新一轮开一版。
func (p *projection) recordVersion(ref contract.DeliverableRef, ts string) {
	var current *versionRecord
	if count := len(p.versions); count > 0 && p.versions[count-1].reworkRound == p.ReworkRound {
		current = &p.versions[count-1]
	} else {
		record := versionRecord{reworkRound: p.ReworkRound, createdAt: ts}
		if p.ReworkRound > 0 && p.Feedback != nil {
			copied := *p.Feedback
			record.feedback = &copied
		}
		if checkpoint, ok := p.done[p.config.Workflow.QCSources.Draft]; ok {
			record.draftPath = checkpoint.Path
		}
		p.versions = append(p.versions, record)
		current = &p.versions[len(p.versions)-1]
	}
	for index := range current.files {
		if current.files[index].Kind == ref.Kind {
			current.files[index] = ref
			return
		}
	}
	current.files = append(current.files, ref)
}

// Versions 快照里的版本列表，旧到新。修改说明由 loadRevisionNotes 填。
func (p *projection) Versions() []contract.DeliverableVersion {
	out := make([]contract.DeliverableVersion, 0, len(p.versions))
	for index, record := range p.versions {
		out = append(out, contract.DeliverableVersion{
			Version:       index + 1,
			ReworkRound:   record.reworkRound,
			CreatedAt:     record.createdAt,
			Files:         append([]contract.DeliverableRef{}, record.files...),
			Feedback:      record.feedback,
			RevisionNotes: record.notes,
		})
	}
	return out
}

// loadRevisionNotes 读各版写作步骤输出里的修改说明（调用方持有 rt.mu）。第 1 版没有退回意见，不读。
func (p *projection) loadRevisionNotes() {
	for index := range p.versions {
		record := &p.versions[index]
		if record.notesRead || record.feedback == nil || record.draftPath == "" {
			continue
		}
		record.notesRead = true
		raw, err := os.ReadFile(record.draftPath)
		if err != nil {
			continue
		}
		var draft struct {
			RevisionNotes []contract.RevisionNote `json:"revisionNotes"`
		}
		if json.Unmarshal(raw, &draft) == nil {
			record.notes = draft.RevisionNotes
		}
	}
}
