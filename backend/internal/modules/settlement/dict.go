package settlement

var statusLabels = map[string]string{
	StatusDraft:     "草稿",
	StatusConfirmed: "已确认",
}

var reconcileLabels = map[string]string{
	ReconcileMatched:   "金额一致",
	ReconcileDifferent: "金额不一致",
	ReconcileUnbilled:  "未结算",
}

// StatusLabel 返回结算单状态的中文名称。
func StatusLabel(status string) string {
	if label, ok := statusLabels[status]; ok {
		return label
	}
	return status
}

// ReconcileLabel 返回对账结果的中文名称。
func ReconcileLabel(result string) string {
	if label, ok := reconcileLabels[result]; ok {
		return label
	}
	return result
}
