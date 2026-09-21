package settlement

import "streetlight/pkg/pagination"

// CreateRequest 新建结算单请求。
type CreateRequest struct {
	Title       string `json:"title" binding:"required,max=128"`
	RepairTeam  string `json:"repair_team" binding:"max=64"`
	PeriodStart string `json:"period_start" binding:"omitempty,max=32"`
	PeriodEnd   string `json:"period_end" binding:"omitempty,max=32"`
	Remark      string `json:"remark" binding:"max=255"`
}

// AddItemRequest 向结算单追加明细请求。
type AddItemRequest struct {
	RepairID uint     `json:"repair_id" binding:"required"`
	Amount   *float64 `json:"amount" binding:"omitempty,min=0"` // 为空时取维修记录上的费用
	Remark   string   `json:"remark" binding:"max=255"`
}

// ListQuery 结算单查询条件。
type ListQuery struct {
	pagination.Params
	Keyword    string `form:"keyword"` // 结算单号 / 标题
	Status     string `form:"status"`
	RepairTeam string `form:"repair_team"`
	StartDate  string `form:"start_date"`
	EndDate    string `form:"end_date"`
}

// Detail 结算单详情: 单头 + 明细列表。
type Detail struct {
	*Settlement
	Items []SettlementItem `json:"items"`
}
