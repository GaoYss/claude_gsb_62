package settlement

import (
	"time"

	"streetlight/pkg/pagination"
)

// CreateRequest 新建结算单请求, 可同时携带初始明细。
type CreateRequest struct {
	Title       string        `json:"title" binding:"required,max=128"`
	PeriodStart string        `json:"period_start" binding:"omitempty,datetime=2006-01-02"`
	PeriodEnd   string        `json:"period_end" binding:"omitempty,datetime=2006-01-02"`
	Operator    string        `json:"operator" binding:"max=64"`
	Remark      string        `json:"remark" binding:"max=255"`
	Items       []ItemRequest `json:"items" binding:"omitempty,dive"`
}

// UpdateRequest 修改结算单(仅草稿可改), 指针字段区分未提交与置空。
type UpdateRequest struct {
	Title       *string        `json:"title" binding:"omitempty,max=128"`
	PeriodStart *string        `json:"period_start" binding:"omitempty,datetime=2006-01-02"`
	PeriodEnd   *string        `json:"period_end" binding:"omitempty,datetime=2006-01-02"`
	Operator    *string        `json:"operator" binding:"omitempty,max=64"`
	Remark      *string        `json:"remark" binding:"omitempty,max=255"`
	Items       *[]ItemRequest `json:"items" binding:"omitempty,dive"`
}

// ItemRequest 结算单明细请求, repair_id 指向一条维修记录, amount 为该笔结算金额。
type ItemRequest struct {
	RepairID uint    `json:"repair_id" binding:"required"`
	Amount   float64 `json:"amount" binding:"min=0"`
	Remark   string  `json:"remark" binding:"max=255"`
}

// ConfirmRequest 确认结算单请求。
type ConfirmRequest struct {
	Remark string `json:"remark" binding:"max=255"`
}

// ListQuery 结算单分页查询条件。
type ListQuery struct {
	pagination.Params
	Status    string `form:"status"`
	Keyword   string `form:"keyword"` // 结算单号 / 标题 / 经办人
	LampID    uint   `form:"lamp_id"` // 履历页: 只看含某盏路灯维修费用的结算单
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// Detail 结算单详情: 结算单头 + 明细(含维修记录快照信息)。
type Detail struct {
	Settlement
	Items []ItemView `json:"items"`
}

// ItemView 结算单明细视图。
type ItemView struct {
	SettlementItem
	// 以下字段来自维修记录, 便于结算单内直接核对, 取不到时为零值。
	FaultType  string     `json:"fault_type"`
	RepairTeam string     `json:"repair_team"`
	Repairman  string     `json:"repairman"`
	StartedAt  *time.Time `json:"started_at"`
	Reconcile  string     `json:"reconcile"` // matched / different
	Difference float64    `json:"difference"`
}

// ReconcileSummary 对账汇总, 供维修履历页展示。
type ReconcileSummary struct {
	TotalItems     int64   `json:"total_items"`     // 已进入结算单的维修笔数(该灯)
	BilledCount    int64   `json:"billed_count"`    // 已结算笔数
	MatchedCount   int64   `json:"matched_count"`   // 金额一致笔数
	DifferentCount int64   `json:"different_count"` // 金额不一致笔数
	UnbilledCount  int64   `json:"unbilled_count"`  // 未结算笔数(按当前履历过滤口径)
	RepairCost     float64 `json:"repair_cost"`     // 维修费用合计(当前过滤口径)
	BilledAmount   float64 `json:"billed_amount"`   // 已登记结算金额合计(去重, 每笔维修取最新一条明细)
}

// CandidateQuery 可结算维修记录候选查询。
type CandidateQuery struct {
	pagination.Params
	Keyword   string `form:"keyword"` // 维修单号 / 故障单号 / 路灯编号 / 维修人员
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// CandidateRepair 是一条可加入结算单的已完工、未结算维修记录。
type CandidateRepair struct {
	RepairID   uint       `json:"repair_id"`
	RepairNo   string     `json:"repair_no"`
	FaultID    uint       `json:"fault_id"`
	FaultNo    string     `json:"fault_no"`
	FaultType  string     `json:"fault_type"`
	LampID     uint       `json:"lamp_id"`
	LampCode   string     `json:"lamp_code"`
	RoadName   string     `json:"road_name"`
	RepairTeam string     `json:"repair_team"`
	Repairman  string     `json:"repairman"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Materials  string     `json:"materials"`
	Cost       float64    `json:"cost"`
}
