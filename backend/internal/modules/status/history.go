package status

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"

	"streetlight/internal/apperr"
	"streetlight/internal/modules/repair"
	"streetlight/internal/modules/settlement"
	"streetlight/pkg/pagination"
)

// 履历行的对账状态。
const (
	reconcileOngoing   = "ongoing"   // 维修中, 尚未进入结算
	reconcileUnsettled = "unsettled" // 已完工未结算
	reconcileSettling  = "settling"  // 已进入草稿结算单, 待确认
	reconcileMatched   = "matched"   // 已确认且金额对平
	reconcileMismatch  = "mismatch"  // 已确认但金额不符
)

// reconcileAmountTolerance 金额对账容差(元), 规避浮点尾差。
const reconcileAmountTolerance = 0.005

// historySortSpec 履历固定按实际发生时间(开工时间)倒序, id 兜底保证翻页稳定。
// 补录的早期记录开工时间更早, 会自动落到履历的正确位置, 与录入先后无关。
var historySortSpec = pagination.SortSpec{
	Allowed: map[string]string{
		"started_at":  "r.started_at",
		"finished_at": "r.finished_at",
		"cost":        "r.cost",
	},
	Default: "r.started_at",
}

// RepairHistoryQuery 是单灯维修履历的查询条件。
type RepairHistoryQuery struct {
	pagination.Params
	FaultType       string `form:"fault_type"`       // 按故障类型收窄
	Result          string `form:"result"`           // 按维修结果收窄
	StartDate       string `form:"start_date"`       // 开工日期起 YYYY-MM-DD
	EndDate         string `form:"end_date"`         // 开工日期止 YYYY-MM-DD
	ReconcileStatus string `form:"reconcile_status"` // 对账状态: unsettled/settling/matched/mismatch
}

// RepairHistorySummary 履历统计(基于当前筛选条件)。
type RepairHistorySummary struct {
	TotalRepairs      int64   `json:"total_repairs"`       // 累计维修次数
	FinishedCount     int64   `json:"finished_count"`      // 已完工次数
	OngoingCount      int64   `json:"ongoing_count"`       // 维修中次数
	AverageDurationHr float64 `json:"average_duration_hr"` // 平均修复时长(小时, 仅已完工)
	TotalCost         float64 `json:"total_cost"`          // 维修费用合计
	MatchedCount      int64   `json:"matched_count"`       // 已对平条数
	MismatchCount     int64   `json:"mismatch_count"`      // 金额不符条数
	SettlingCount     int64   `json:"settling_count"`      // 结算中条数
	UnsettledCount    int64   `json:"unsettled_count"`     // 未结算条数(含维修中)
}

// RepairHistoryRow 履历中的一次维修: 维修记录 + 故障信息 + 结算对账信息。
type RepairHistoryRow struct {
	RepairID        uint       `json:"repair_id"`
	RepairNo        string     `json:"repair_no"`
	FaultID         uint       `json:"fault_id"`
	FaultNo         string     `json:"fault_no"`
	FaultType       string     `json:"fault_type"`
	FaultLevel      string     `json:"fault_level"`
	FaultStatus     string     `json:"fault_status"`
	RepairTeam      string     `json:"repair_team"`
	Repairman       string     `json:"repairman"`
	ContactPhone    string     `json:"contact_phone"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	Status          string     `json:"status"`
	Result          string     `json:"result"`
	Content         string     `json:"content"`
	Materials       string     `json:"materials"` // 更换的灯具与耗材
	Cost            float64    `json:"cost"`      // 维修费用
	Remark          string     `json:"remark"`
	DurationMinutes *int64     `json:"duration_minutes"` // 维修用时(分钟)
	CreatedAt       time.Time  `json:"created_at"`       // 录入时间, 补录记录会晚于发生时间

	SettlementID    *uint    `json:"settlement_id"`
	SettleNo        string   `json:"settle_no"`
	SettleStatus    string   `json:"settle_status"`
	SettledAmount   *float64 `json:"settled_amount"`
	AmountDiff      *float64 `json:"amount_diff"` // 结算金额 - 维修费用
	ReconcileStatus string   `json:"reconcile_status"`
}

// RepairHistoryPage 单灯维修履历响应: 路灯信息 + 统计 + 分页明细。
type RepairHistoryPage struct {
	Lamp       any                  `json:"lamp"`
	Summary    RepairHistorySummary `json:"summary"`
	Items      []RepairHistoryRow   `json:"items"`
	Total      int64                `json:"total"`
	Page       int                  `json:"page"`
	PageSize   int                  `json:"page_size"`
	TotalPages int                  `json:"total_pages"`
}

// historyRowDAO 承接三表 JOIN 的原始列。
type historyRowDAO struct {
	RepairID     uint
	RepairNo     string
	FaultID      uint
	FaultNo      string
	FaultType    string
	FaultLevel   string
	FaultStatus  string
	RepairTeam   string
	Repairman    string
	ContactPhone string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Status       string
	Result       string
	Content      string
	Materials    string
	Cost         float64
	Remark       string
	CreatedAt    time.Time

	ItemID       *uint
	SettlementID *uint
	SettleNo     *string
	SettleStatus *string
	Amount       *float64
}

// RepairHistory 查询单盏路灯的完整维修履历, 按实际开工时间倒序分页, 并返回同口径统计。
func (s *Service) RepairHistory(ctx context.Context, lampID uint, query RepairHistoryQuery) (*RepairHistoryPage, error) {
	device, err := s.lamps.GetByID(ctx, lampID)
	if err != nil {
		return nil, err
	}

	page := pagination.Parse(query.Params, historySortSpec)
	filter, err := buildHistoryFilter(query)
	if err != nil {
		return nil, err
	}

	base := func() *gorm.DB {
		return s.applyHistoryScope(s.db.WithContext(ctx).
			Table("repair AS r").
			Joins("LEFT JOIN fault AS f ON f.id = r.fault_id").
			Joins("LEFT JOIN settlement_item AS si ON si.repair_id = r.id").
			Joins("LEFT JOIN settlement AS s ON s.id = si.settlement_id").
			Where("r.lamp_id = ?", lampID), filter)
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, fmt.Errorf("统计维修履历失败: %w", err)
	}

	summary, err := s.repairHistoryAggregate(ctx, lampID, filter)
	if err != nil {
		return nil, err
	}

	rows := make([]historyRowDAO, 0, page.PageSize)
	selectColumns := strings.Join([]string{
		"r.id AS repair_id", "r.repair_no", "r.fault_id", "r.fault_no",
		"f.fault_type", "f.fault_level", "f.status AS fault_status",
		"r.repair_team", "r.repairman", "r.contact_phone",
		"r.started_at", "r.finished_at", "r.status", "r.result",
		"r.content", "r.materials", "r.cost", "r.remark", "r.created_at",
		"si.id AS item_id", "si.settlement_id", "s.settle_no", "s.status AS settle_status",
		"si.amount AS amount",
	}, ", ")
	if err := base().Select(selectColumns).
		Order(historyOrderClause(page)).
		Offset(page.Offset()).Limit(page.Limit()).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询维修履历失败: %w", err)
	}

	items := make([]RepairHistoryRow, 0, len(rows))
	for _, item := range rows {
		items = append(items, toHistoryRow(item))
	}

	totalPages := 0
	if page.PageSize > 0 {
		totalPages = int((total + int64(page.PageSize) - 1) / int64(page.PageSize))
	}
	return &RepairHistoryPage{
		Lamp:       device,
		Summary:    summary,
		Items:      items,
		Total:      total,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalPages: totalPages,
	}, nil
}

// applyHistoryScope 拼装履历的故障类型/时间段/对账状态筛选。
func (s *Service) applyHistoryScope(statement *gorm.DB, filter historyFilter) *gorm.DB {
	if value := strings.TrimSpace(filter.FaultType); value != "" {
		statement = statement.Where("f.fault_type = ?", value)
	}
	if value := strings.TrimSpace(filter.Result); value != "" {
		statement = statement.Where("r.result = ?", value)
	}
	if filter.StartedFrom != nil {
		statement = statement.Where("r.started_at >= ?", *filter.StartedFrom)
	}
	if filter.StartedTo != nil {
		statement = statement.Where("r.started_at < ?", *filter.StartedTo)
	}
	switch filter.ReconcileStatus {
	case reconcileMatched:
		statement = statement.
			Where("s.status = ?", settlement.StatusConfirmed).
			Where("ABS(COALESCE(si.amount, 0) - COALESCE(r.cost, 0)) < ?", reconcileAmountTolerance)
	case reconcileMismatch:
		statement = statement.
			Where("s.status = ?", settlement.StatusConfirmed).
			Where("ABS(COALESCE(si.amount, 0) - COALESCE(r.cost, 0)) >= ?", reconcileAmountTolerance)
	case reconcileSettling:
		statement = statement.Where("s.status = ?", settlement.StatusDraft)
	case reconcileUnsettled:
		statement = statement.Where("si.id IS NULL")
	}
	return statement
}

// repairHistoryAggregate 在数据库内一次性完成履历统计, 数据量增长也不占用应用内存。
func (s *Service) repairHistoryAggregate(ctx context.Context, lampID uint, filter historyFilter) (RepairHistorySummary, error) {
	// 平均修复用时在 sqlite / postgres 的时间差函数不同, 按方言生成表达式,
	// 统计仍全部下推到数据库(走 repair(lamp_id, started_at) 索引)。
	var avgExpr string
	switch s.db.Dialector.Name() {
	case "postgres":
		avgExpr = "AVG(CASE WHEN r.status = 'finished' AND r.finished_at IS NOT NULL " +
			"THEN EXTRACT(EPOCH FROM (r.finished_at - r.started_at)) / 3600.0 END)"
	default:
		avgExpr = "AVG(CASE WHEN r.status = 'finished' AND r.finished_at IS NOT NULL " +
			"THEN (julianday(r.finished_at) - julianday(r.started_at)) * 24 END)"
	}

	type aggRow struct {
		Total        int64
		Finished     int64
		Ongoing      int64
		AverageHr    *float64
		TotalCost    float64
		Matched      int64
		Mismatch     int64
		Settling     int64
		NoSettlement int64
	}
	var agg aggRow

	statement := s.applyHistoryScope(s.db.WithContext(ctx).
		Table("repair AS r").
		Joins("LEFT JOIN fault AS f ON f.id = r.fault_id").
		Joins("LEFT JOIN settlement_item AS si ON si.repair_id = r.id").
		Joins("LEFT JOIN settlement AS s ON s.id = si.settlement_id").
		Where("r.lamp_id = ?", lampID), filter)

	err := statement.Select(
		"COUNT(*) AS total, "+
			"SUM(CASE WHEN r.status = ? THEN 1 ELSE 0 END) AS finished, "+
			"SUM(CASE WHEN r.status = ? THEN 1 ELSE 0 END) AS ongoing, "+
			avgExpr+" AS average_hr, "+
			"COALESCE(SUM(r.cost), 0) AS total_cost, "+
			"SUM(CASE WHEN s.status = ? AND ABS(COALESCE(si.amount, 0) - COALESCE(r.cost, 0)) < ? THEN 1 ELSE 0 END) AS matched, "+
			"SUM(CASE WHEN s.status = ? AND ABS(COALESCE(si.amount, 0) - COALESCE(r.cost, 0)) >= ? THEN 1 ELSE 0 END) AS mismatch, "+
			"SUM(CASE WHEN s.status = ? THEN 1 ELSE 0 END) AS settling, "+
			"SUM(CASE WHEN si.id IS NULL THEN 1 ELSE 0 END) AS no_settlement",
		repair.StatusFinished, repair.StatusOngoing,
		settlement.StatusConfirmed, reconcileAmountTolerance,
		settlement.StatusConfirmed, reconcileAmountTolerance,
		settlement.StatusDraft,
	).Scan(&agg).Error
	if err != nil {
		return RepairHistorySummary{}, fmt.Errorf("聚合维修履历统计失败: %w", err)
	}

	summary := RepairHistorySummary{
		TotalRepairs:   agg.Total,
		FinishedCount:  agg.Finished,
		OngoingCount:   agg.Ongoing,
		TotalCost:      round2(agg.TotalCost),
		MatchedCount:   agg.Matched,
		MismatchCount:  agg.Mismatch,
		SettlingCount:  agg.Settling,
		UnsettledCount: agg.NoSettlement,
	}
	if agg.AverageHr != nil {
		summary.AverageDurationHr = round2(*agg.AverageHr)
	}
	return summary, nil
}

// historyOrderClause 生成履历排序片段, 兜底列使用 r.id 避免 JOIN 后列名歧义。
func historyOrderClause(page pagination.Query) string {
	column := page.SortColumn
	if column == "" {
		column = "r.started_at"
	}
	if page.Descending {
		return column + " DESC, r.id DESC"
	}
	return column + " ASC, r.id ASC"
}

// historyFilter 是解析后的履历筛选条件。
type historyFilter struct {
	FaultType       string
	Result          string
	StartedFrom     *time.Time
	StartedTo       *time.Time
	ReconcileStatus string
}

func buildHistoryFilter(query RepairHistoryQuery) (historyFilter, error) {
	filter := historyFilter{
		FaultType:       strings.TrimSpace(query.FaultType),
		Result:          strings.TrimSpace(query.Result),
		ReconcileStatus: strings.TrimSpace(query.ReconcileStatus),
	}
	if filter.Result != "" && !repair.IsValidResult(filter.Result) {
		return filter, apperr.BadRequest("非法的维修结果: %s", filter.Result)
	}
	switch filter.ReconcileStatus {
	case "", reconcileUnsettled, reconcileSettling, reconcileMatched, reconcileMismatch:
	default:
		return filter, apperr.BadRequest("非法的对账状态: %s", filter.ReconcileStatus)
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		from, err := parseHistoryDay(value)
		if err != nil {
			return filter, err
		}
		filter.StartedFrom = &from
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		to, err := parseHistoryDay(value)
		if err != nil {
			return filter, err
		}
		to = to.AddDate(0, 0, 1)
		filter.StartedTo = &to
	}
	if filter.StartedFrom != nil && filter.StartedTo != nil && filter.StartedTo.Before(*filter.StartedFrom) {
		return filter, apperr.BadRequest("结束日期不能早于开始日期")
	}
	return filter, nil
}

func parseHistoryDay(value string) (time.Time, error) {
	date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.Local)
	if err != nil {
		return time.Time{}, apperr.BadRequest("日期格式应为 YYYY-MM-DD, 当前值: %s", value)
	}
	return date, nil
}

// toHistoryRow 把 JOIN 结果转换为履历行并计算用时与对账状态。
func toHistoryRow(dao historyRowDAO) RepairHistoryRow {
	row := RepairHistoryRow{
		RepairID:     dao.RepairID,
		RepairNo:     dao.RepairNo,
		FaultID:      dao.FaultID,
		FaultNo:      dao.FaultNo,
		FaultType:    dao.FaultType,
		FaultLevel:   dao.FaultLevel,
		FaultStatus:  dao.FaultStatus,
		RepairTeam:   dao.RepairTeam,
		Repairman:    dao.Repairman,
		ContactPhone: dao.ContactPhone,
		StartedAt:    dao.StartedAt,
		FinishedAt:   dao.FinishedAt,
		Status:       dao.Status,
		Result:       dao.Result,
		Content:      dao.Content,
		Materials:    dao.Materials,
		Cost:         dao.Cost,
		Remark:       dao.Remark,
		CreatedAt:    dao.CreatedAt,
	}
	if dao.FinishedAt != nil {
		minutes := int64(dao.FinishedAt.Sub(dao.StartedAt).Minutes())
		if minutes < 0 {
			minutes = 0
		}
		row.DurationMinutes = &minutes
	}

	if dao.SettlementID != nil && dao.Amount != nil {
		row.SettlementID = dao.SettlementID
		row.SettledAmount = dao.Amount
		if dao.SettleNo != nil {
			row.SettleNo = *dao.SettleNo
		}
		if dao.SettleStatus != nil {
			row.SettleStatus = *dao.SettleStatus
		}
		diff := roundSigned2(*dao.Amount - dao.Cost)
		row.AmountDiff = &diff
	}
	row.ReconcileStatus = classifyReconcile(dao.Status, dao.SettleStatus, dao.Amount, dao.Cost)
	return row
}

// roundSigned2 保留两位小数但不改变符号(对账差额可能为负)。
func roundSigned2(value float64) float64 {
	return math.Round(value*100) / 100
}

// classifyReconcile 依据维修状态与结算情况判定对账状态。
func classifyReconcile(repairStatus string, settleStatus *string, settledAmount *float64, cost float64) string {
	switch {
	case repairStatus != repair.StatusFinished:
		return reconcileOngoing
	case settleStatus == nil:
		return reconcileUnsettled
	case *settleStatus == settlement.StatusDraft:
		return reconcileSettling
	default:
		diff := *settledAmount - cost
		if diff < 0 {
			diff = -diff
		}
		if diff < reconcileAmountTolerance {
			return reconcileMatched
		}
		return reconcileMismatch
	}
}
