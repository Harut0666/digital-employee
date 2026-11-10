// 面向人的暂停说明：标题和下一步做什么，读取时按暂停代码和详情生成，不写入事件，
// 所以文案调整后旧工作区的任务也能看到新说明。
package engine

import "digital-employee/internal/contract"

const (
	operatorDecides = "由操作员决定重试或终止。"
	anyoneRetries   = "可以重试；问题解决不了时由操作员终止任务。"
)

// pauseTitles 暂停代码对应的标题和下一步。
var pauseTitles = map[contract.PauseCode][2]string{
	contract.PauseMissingSales:         {"缺少销售明细", "请上传销售明细后继续。"},
	contract.PauseMissingDimensions:    {"缺少维表", "请上传完整的销售明细文件后继续。"},
	contract.PausePeriodOutOfRange:     {"统计周期超出数据范围", "请改选范围内的周次，或补充对应日期的销售数据。"},
	contract.PausePeriodIncomplete:     {"销售数据不完整", "可以补充缺失日期的数据，或按已有数据继续。"},
	contract.PauseMinutesMissing:       {"缺少本周纪要", "可以补充纪要，或选择无纪要继续。"},
	contract.PauseMinutesWeekMismatch:  {"纪要周次不符", "可以上传正确的纪要，或选择无纪要继续。"},
	contract.PauseInvalidInput:         {"材料或表单有问题", "请按说明修改后重新提交。"},
	contract.PauseMissingList:          {"缺少专利清单", "请上传专利清单（CSV）后继续。"},
	contract.PauseListDirection:        {"清单与方向不符", "请改方向或换清单。"},
	contract.PauseInsufficientMaterial: {"资料不足", "可以补充资料；纳入件数偏少时也可以按现有资料继续，没有可用资料时只能补充资料或终止。"},
	contract.PauseMaterialIssue:        {"材料有问题", "请重新上传有问题的材料，补充后任务继续。"},
	contract.PauseToolFailed:           {"工具调用失败", anyoneRetries},
	contract.PauseModelUnavailable:     {"模型不可用", "请到设置里检查模型连接，然后重试。"},
	contract.PauseLimitExceeded:        {"已达执行上限", operatorDecides},
	contract.PauseQcExhausted:          {"质检未通过", operatorDecides},
	contract.PauseAwaitingReview:       {"待审核", "请审核交付物，决定通过或退回。"},
}

// modelErrorText 模型不可用时，按错误类别给出的标题和下一步。
var modelErrorText = map[contract.ModelErrorKind][2]string{
	contract.ModelErrorAuth:    {"模型认证失败", "请到设置里检查模型连接的 Key，保存后重试。"},
	contract.ModelErrorNetwork: {"模型连接不上", "请检查网络和模型连接的地址，恢复后重试。"},
	contract.ModelErrorQuota:   {"模型限流或欠费", "请稍等片刻再重试；一直失败时到模型服务商处查看余额和额度。"},
	contract.ModelErrorServer:  {"模型服务出错", "请稍后重试；一直失败时到设置里换一个模型连接。"},
}

// judgmentFailureTitles 判断步骤失败时，比暂停代码更具体的标题。
var judgmentFailureTitles = map[contract.JudgmentFailure]string{
	contract.FailStepRounds:    "步骤轮次已达上限",
	contract.FailStepDuration:  "步骤时长已达上限",
	contract.FailTaskCalls:     "模型调用次数已达上限",
	contract.FailTaskDuration:  "任务总时长已达上限",
	contract.FailModelError:    "模型调用失败",
	contract.FailNoOutput:      "模型没有给出结果",
	contract.FailInvalidOutput: "模型输出不符合要求",
}

// limitTitles 任务级预算用尽时的标题。
var limitTitles = map[string]string{
	"modelCalls":  "模型调用次数已达上限",
	"durationSec": "任务总时长已达上限",
}

// describePause 暂停的标题和下一步。
func describePause(state contract.TaskState, pause *contract.PauseReason) (title, resolution string) {
	if pause.Code == contract.PauseReworkNeedsHuman {
		title = "退回意见超出自动处理范围"
		if state == contract.StateAwaitingOperator {
			// 旧工作区里这类任务停在待人工处理
			return title, operatorDecides
		}
		return title, "可以修改意见后重新退回，也可以直接通过或终止任务。"
	}
	text, known := pauseTitles[pause.Code]
	if !known {
		return contract.StateLabels[state], "请按说明处理。"
	}
	title, resolution = text[0], text[1]
	if kind, ok := pause.Detail["modelError"].(string); ok {
		if specific, found := modelErrorText[contract.ModelErrorKind(kind)]; found {
			return specific[0], specific[1]
		}
	}
	if failure, ok := pause.Detail["failure"].(string); ok {
		if specific, found := judgmentFailureTitles[contract.JudgmentFailure(failure)]; found {
			title = specific
		}
	}
	if limit, ok := pause.Detail["limit"].(string); ok {
		if specific, found := limitTitles[limit]; found {
			title = specific
		}
	}
	return title, resolution
}

// presentPause 快照里的暂停原因：在事件里的暂停原因上补齐读取时生成的内容。
func presentPause(state contract.TaskState, pause *contract.PauseReason, actions []contract.TaskAction) *contract.PauseReason {
	if pause == nil {
		return nil
	}
	presented := *pause
	presented.Title, presented.Resolution = describePause(state, pause)
	presented.Options = commandsOf(actions)
	return &presented
}

// statusText 任务列表里显示的状态：暂停时说明卡在什么上，其余显示状态名。
func statusText(state contract.TaskState, pause *contract.PauseReason) string {
	if contract.IsPaused(state) && pause != nil {
		title, _ := describePause(state, pause)
		return title
	}
	return contract.StateLabels[state]
}
