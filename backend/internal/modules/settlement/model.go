package settlement

import "time"

// 结算单状态。
const (
	StatusDraft     = "draft"     // 草稿
	StatusConfirmed = "confirmed" // 已确认
)

// 对账结果: 履历中的维修费用与结算单明细逐条核对的结论。
const (
	ReconcileMatched   = "matched"   // 金额一致
	ReconcileDifferent = "different" // 金额不一致
	ReconcileUnbilled  = "unbilled"  // 未进入结算单
)

// Statuses 返回全部结算单状态。
func Statuses() []string {
	return []string{StatusDraft, StatusConfirmed}
}

// IsValidStatus 校验结算单状态取值。
func IsValidStatus(status string) bool {
	for _, item := range Statuses() {
		if item == status {
			return true
		}
	}
	return false
}

// Settlement 结算单: 一张单覆盖一个结算周期内的多笔维修费用。
// 金额以明细(SettlementItem)逐条登记, 每条明细对应一条维修记录,
// 因此维修履历里的费用可以与结算单金额逐条对上。
type Settlement struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	SettleNo    string     `gorm:"size:64;uniqueIndex;not null" json:"settle_no"`
	Title       string     `gorm:"size:128;not null" json:"title"`
	PeriodStart *time.Time `gorm:"type:date;index" json:"period_start"`
	PeriodEnd   *time.Time `gorm:"type:date;index" json:"period_end"`
	Status      string     `gorm:"size:32;index;not null;default:draft" json:"status"`
	Operator    string     `gorm:"size:64" json:"operator"`
	Remark      string     `gorm:"size:255" json:"remark"`
	ConfirmedAt *time.Time `json:"confirmed_at"`

	// TotalAmount 为明细金额的应用层汇总(列表展示用), 始终以明细之和为准重算。
	TotalAmount float64 `gorm:"not null;default:0" json:"total_amount"`
	// ItemCount 为明细条数, 避免列表场景逐条 count。
	ItemCount int `gorm:"not null;default:0" json:"item_count"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Settlement) TableName() string { return "settlement" }

// SettlementItem 结算单明细: 一条维修记录全局至多结算一次(repair_id 唯一索引),
// 因此维修履历中的每一笔费用都能与唯一一条结算单明细逐条对上。
type SettlementItem struct {
	ID           uint    `gorm:"primaryKey" json:"id"`
	SettlementID uint    `gorm:"index;not null" json:"settlement_id"`
	RepairID     uint    `gorm:"uniqueIndex;not null" json:"repair_id"`
	RepairNo     string  `gorm:"size:64;index" json:"repair_no"`
	FaultID      uint    `gorm:"index" json:"fault_id"`
	FaultNo      string  `gorm:"size:64" json:"fault_no"`
	LampID       uint    `gorm:"index" json:"lamp_id"`
	LampCode     string  `gorm:"size:64;index" json:"lamp_code"`
	Amount       float64 `gorm:"not null;default:0" json:"amount"`
	// RepairCost 冗余登记时的维修费用快照, 便于结算单内直接展示差异, 履历对账仍以维修记录现值为准。
	RepairCost float64   `gorm:"not null;default:0" json:"repair_cost"`
	Remark     string    `gorm:"size:255" json:"remark"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName 指定表名。
func (SettlementItem) TableName() string { return "settlement_item" }
