package settlement

import "time"

// 结算单状态。
const (
	StatusDraft     = "draft"     // 草稿(可继续增删明细)
	StatusConfirmed = "confirmed" // 已确认(金额锁定, 作为对账基准)
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

// Settlement 结算单: 按周期或按班组汇总维修费用, 一张结算单包含多条明细。
type Settlement struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	SettleNo    string     `gorm:"size:64;uniqueIndex;not null" json:"settle_no"`
	Title       string     `gorm:"size:128;not null" json:"title"`
	RepairTeam  string     `gorm:"size:64;index" json:"repair_team"`
	PeriodStart *time.Time `gorm:"type:date" json:"period_start"`
	PeriodEnd   *time.Time `gorm:"type:date" json:"period_end"`
	Status      string     `gorm:"size:32;index;not null;default:draft" json:"status"`
	// TotalAmount 为各明细金额之和, 落库以便与履历维修费用直接核对。
	TotalAmount float64    `gorm:"not null;default:0" json:"total_amount"`
	Remark      string     `gorm:"size:255" json:"remark"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (Settlement) TableName() string { return "settlement" }

// SettlementItem 结算单明细: 每条明细唯一对应一条维修记录, 金额与该次维修费用逐条对账。
type SettlementItem struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	SettlementID uint      `gorm:"index;not null" json:"settlement_id"`
	RepairID     uint      `gorm:"uniqueIndex;not null" json:"repair_id"` // 一条维修记录最多被结算一次
	RepairNo     string    `gorm:"size:64;index" json:"repair_no"`
	LampID       uint      `gorm:"index" json:"lamp_id"`
	LampCode     string    `gorm:"size:64;index" json:"lamp_code"`
	FaultNo      string    `gorm:"size:64" json:"fault_no"`
	Amount       float64   `gorm:"not null;default:0" json:"amount"` // 结算金额, 应与 repair.cost 一致
	Remark       string    `gorm:"size:255" json:"remark"`
	CreatedAt    time.Time `json:"created_at"`
}

// TableName 指定表名。
func (SettlementItem) TableName() string { return "settlement_item" }
