package status

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"streetlight/internal/apperr"
	"streetlight/internal/modules/fault"
	"streetlight/internal/modules/lamp"
	"streetlight/internal/modules/repair"
	"streetlight/internal/modules/settlement"
	"streetlight/pkg/pagination"
)

// backfillThreshold: 录入时间晚于开工时间超过该阈值视为补录的早期记录。
const backfillThreshold = 24 * time.Hour

// historySortSpec 履历固定按维修实际发生时间排序(仅允许开工/完工时间切换), 保证补录记录按实际发生时间归位。
var historySortSpec = pagination.SortSpec{
	Allowed: map[string]string{
		"started_at":  "repair.started_at",
		"finished_at": "repair.finished_at",
	},
	Default: "repair.started_at",
}

// HistoryQuery 单灯维修履历查询条件。
type HistoryQuery struct {
	pagination.Params
	FaultType string `form:"fault_type"` // 单个类型或逗号分隔的多个类型
	TimeField string `form:"time_field"` // 时间段过滤与排序依据: started_at(默认) / finished_at
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// LampHistory 单盏路灯的完整维修履历: 档案 + 累计统计 + 历次维修明细分页。
type LampHistory struct {
	Lamp     lamp.Lamp      `json:"lamp"`
	Summary  HistorySummary `json:"summary"`
	Items    []HistoryItem  `json:"items"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

// HistorySummary 履历统计。除过滤口径外始终返回该灯的全量累计值, 顶部统计卡不随筛选波动。
type HistorySummary struct {
	// 全量累计(不受故障类型/时间段过滤影响)。
	LifetimeRepairCount       int64   `json:"lifetime_repair_count"`
	LifetimeFinishedCount     int64   `json:"lifetime_finished_count"`
	LifetimeFaultCount        int64   `json:"lifetime_fault_count"`
	LifetimeAverageDurationHr float64 `json:"lifetime_average_duration_hours"`
	LifetimeTotalCost         float64 `json:"lifetime_total_cost"`
	// 当前过滤口径。
	RepairTotal       int64   `json:"repair_total"`
	FinishedTotal     int64   `json:"finished_total"`
	OngoingTotal      int64   `json:"ongoing_total"`
	AverageDurationHr float64 `json:"average_duration_hours"`
	TotalCost         float64 `json:"total_cost"`
	// 对账统计(当前过滤口径)。
	BilledCount    int64   `json:"billed_count"`
	MatchedCount   int64   `json:"matched_count"`
	DifferentCount int64   `json:"different_count"`
	UnbilledCount  int64   `json:"unbilled_count"`
	BilledAmount   float64 `json:"billed_amount"`
}

// HistoryItem 履历中的一行: 一次维修过程及其所属故障、结算对账信息。
type HistoryItem struct {
	RepairID        uint       `json:"repair_id"`
	RepairNo        string     `json:"repair_no"`
	FaultID         uint       `json:"fault_id"`
	FaultNo         string     `json:"fault_no"`
	FaultType       string     `json:"fault_type"`
	FaultLevel      string     `json:"fault_level"`
	FaultStatus     string     `json:"fault_status"`
	ReportedAt      time.Time  `json:"reported_at"`
	ClosedAt        *time.Time `json:"closed_at"`
	Repairman       string     `json:"repairman"`
	RepairTeam      string     `json:"repair_team"`
	ContactPhone    string     `json:"contact_phone"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	DurationMinutes *int64     `json:"duration_minutes"`
	Status          string     `json:"status"`
	Result          string     `json:"result"`
	Content         string     `json:"content"`
	Materials       string     `json:"materials"`
	Cost            float64    `json:"cost"`
	Remark          string     `json:"remark"`
	IsBackfilled    bool       `json:"is_backfilled"`
	SettlementID    uint       `json:"settlement_id"`
	SettleNo        string     `json:"settle_no"`
	SettleStatus    string     `json:"settle_status"`
	BilledAmount    float64    `json:"billed_amount"`
	Reconcile       string     `json:"reconcile"`
	Difference      float64    `json:"difference"`
}

// historyFilter 解析后的履历过滤条件。
type historyFilter struct {
	FaultTypes []string
	TimeColumn string // repair.started_at 或 repair.finished_at
	From       *time.Time
	To         *time.Time
}

// LampHistory 查询单盏路灯的维修履历, 历次维修按实际发生时间(开工时间)倒序分页。
func (s *Service) LampHistory(ctx context.Context, lampID uint, query HistoryQuery) (*LampHistory, error) {
	device, err := s.lamps.GetByID(ctx, lampID)
	if err != nil {
		return nil, err
	}

	filter, page, err := parseHistoryQuery(query)
	if err != nil {
		return nil, err
	}

	lifetime, err := s.aggregateHistory(ctx, lampID, historyFilter{})
	if err != nil {
		return nil, err
	}
	filtered, err := s.aggregateHistory(ctx, lampID, filter)
	if err != nil {
		return nil, err
	}

	var totalFaults int64
	if err := s.db.WithContext(ctx).Model(&fault.Fault{}).
		Where("lamp_id = ?", lampID).Count(&totalFaults).Error; err != nil {
		return nil, err
	}

	records := make([]repair.Repair, 0, page.PageSize)
	if err := s.historyBase(ctx, lampID, filter).
		Order(historyOrderClause(page)).
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&records).Error; err != nil {
		return nil, err
	}

	items, err := s.buildHistoryItems(ctx, records)
	if err != nil {
		return nil, err
	}

	summary := HistorySummary{
		LifetimeRepairCount:       lifetime.total,
		LifetimeFinishedCount:     lifetime.finished,
		LifetimeFaultCount:        totalFaults,
		LifetimeAverageDurationHr: round2(lifetime.avgDurationHr),
		LifetimeTotalCost:         round2(lifetime.cost),
		RepairTotal:               filtered.total,
		FinishedTotal:             filtered.finished,
		OngoingTotal:              filtered.total - filtered.finished,
		AverageDurationHr:         round2(filtered.avgDurationHr),
		TotalCost:                 round2(filtered.cost),
		BilledCount:               filtered.billed,
		MatchedCount:              filtered.billed - filtered.different,
		DifferentCount:            filtered.different,
		UnbilledCount:             filtered.total - filtered.billed,
		BilledAmount:              round2(filtered.billedAmount),
	}

	return &LampHistory{
		Lamp:     *device,
		Summary:  summary,
		Items:    items,
		Total:    filtered.total,
		Page:     page.Page,
		PageSize: page.PageSize,
	}, nil
}

// historyAggregate 是一次履历聚合的结果。
type historyAggregate struct {
	total         int64
	finished      int64
	cost          float64
	avgDurationHr float64
	billed        int64
	different     int64
	billedAmount  float64
}

// historyBase 拼装履历基础查询: repair JOIN fault, 应用故障类型与开工时间过滤。
// 排序与分页由调用方追加; 每次调用返回新语句避免条件累积。
func (s *Service) historyBase(ctx context.Context, lampID uint, filter historyFilter) *gorm.DB {
	statement := s.db.WithContext(ctx).
		Model(&repair.Repair{}).
		Joins("JOIN fault ON fault.id = repair.fault_id").
		Where("repair.lamp_id = ?", lampID)
	if len(filter.FaultTypes) > 0 {
		statement = statement.Where("fault.fault_type IN ?", filter.FaultTypes)
	}
	if filter.From != nil {
		statement = statement.Where(filter.TimeColumn+" >= ?", *filter.From)
	}
	if filter.To != nil {
		// 按完工时间过滤时, NULL(维修中)记录天然落在区间之外, 符合"时间段内完工"的预期。
		statement = statement.Where(filter.TimeColumn+" < ?", *filter.To)
	}
	return statement
}

// aggregateHistory 在数据库内完成次数/费用/平均修复时长/对账的聚合, 避免把几千条记录取回应用层。
func (s *Service) aggregateHistory(ctx context.Context, lampID uint, filter historyFilter) (historyAggregate, error) {
	var result historyAggregate

	type row struct {
		Total        int64
		Finished     int64
		Cost         float64
		AvgDuration  *float64
		Billed       int64
		Different    int64
		BilledAmount float64
	}
	var data row

	// 平均修复时长按数据库方言在 SQL 内计算:
	// sqlite 用 julianday, postgres 用 EXTRACT(EPOCH), 口径均为小时。
	var avgExpr string
	switch s.db.Dialector.Name() {
	case "postgres":
		avgExpr = "AVG(EXTRACT(EPOCH FROM (repair.finished_at - repair.started_at)) / 3600)"
	default:
		avgExpr = "AVG((julianday(repair.finished_at) - julianday(repair.started_at)) * 24)"
	}

	err := s.historyBase(ctx, lampID, filter).
		Joins("LEFT JOIN settlement_item ON settlement_item.repair_id = repair.id").
		Select(`
			COUNT(*) AS total,
			COUNT(repair.finished_at) AS finished,
			COALESCE(SUM(repair.cost), 0) AS cost,
			`+avgExpr+` AS avg_duration,
			COUNT(settlement_item.id) AS billed,
			COALESCE(SUM(CASE WHEN ABS(settlement_item.amount - repair.cost) > ? THEN 1 ELSE 0 END), 0) AS different,
			COALESCE(SUM(settlement_item.amount), 0) AS billed_amount`,
			settlement.CostTolerance,
		).
		Scan(&data).Error
	if err != nil {
		return result, err
	}

	result.total = data.Total
	result.finished = data.Finished
	result.cost = data.Cost
	result.billed = data.Billed
	result.different = data.Different
	result.billedAmount = data.BilledAmount
	if data.AvgDuration != nil {
		if *data.AvgDuration < 0 {
			*data.AvgDuration = 0
		}
		result.avgDurationHr = *data.AvgDuration
	}
	return result, nil
}

// buildHistoryItems 为当前页维修记录补齐故障信息与结算对账信息。
func (s *Service) buildHistoryItems(ctx context.Context, records []repair.Repair) ([]HistoryItem, error) {
	items := make([]HistoryItem, 0, len(records))
	if len(records) == 0 {
		return items, nil
	}

	faultIDs := make([]uint, 0, len(records))
	repairIDs := make([]uint, 0, len(records))
	for _, record := range records {
		faultIDs = append(faultIDs, record.FaultID)
		repairIDs = append(repairIDs, record.ID)
	}

	faults := make([]fault.Fault, 0, len(faultIDs))
	if err := s.db.WithContext(ctx).Where("id IN ?", faultIDs).Find(&faults).Error; err != nil {
		return nil, err
	}
	faultMap := make(map[uint]fault.Fault, len(faults))
	for _, entity := range faults {
		faultMap[entity.ID] = entity
	}

	billMap, err := s.settlements.ItemsByRepairIDs(ctx, repairIDs)
	if err != nil {
		return nil, err
	}
	settleIDs := make([]uint, 0, len(billMap))
	for _, bill := range billMap {
		settleIDs = append(settleIDs, bill.SettlementID)
	}
	settleMap, err := s.settlements.SettlementsByIDs(ctx, settleIDs)
	if err != nil {
		return nil, err
	}

	for _, record := range records {
		record.FillDuration()
		item := HistoryItem{
			RepairID:        record.ID,
			RepairNo:        record.RepairNo,
			FaultID:         record.FaultID,
			FaultNo:         record.FaultNo,
			Repairman:       record.Repairman,
			RepairTeam:      record.RepairTeam,
			ContactPhone:    record.ContactPhone,
			StartedAt:       record.StartedAt,
			FinishedAt:      record.FinishedAt,
			DurationMinutes: record.DurationMinutes,
			Status:          record.Status,
			Result:          record.Result,
			Content:         record.Content,
			Materials:       record.Materials,
			Cost:            record.Cost,
			Remark:          record.Remark,
			IsBackfilled:    record.CreatedAt.Sub(record.StartedAt) > backfillThreshold,
			Reconcile:       settlement.ReconcileUnbilled,
		}
		if entity, ok := faultMap[record.FaultID]; ok {
			item.FaultType = entity.FaultType
			item.FaultLevel = entity.FaultLevel
			item.FaultStatus = entity.Status
			item.ReportedAt = entity.ReportedAt
			item.ClosedAt = entity.ClosedAt
		}
		if bill, ok := billMap[record.ID]; ok {
			item.SettlementID = bill.SettlementID
			item.BilledAmount = bill.Amount
			item.Difference = round2(bill.Amount - record.Cost)
			item.Reconcile = settlement.ReconcileState(record.Cost, bill.Amount, true)
			if header, exists := settleMap[bill.SettlementID]; exists {
				item.SettleNo = header.SettleNo
				item.SettleStatus = header.Status
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// historyOrderClause 返回履历排序片段。列名来自白名单; JOIN 多表后必须全限定列名,
// 并以 repair.id 兜底, 保证补录产生相同开工时间时翻页顺序稳定。
func historyOrderClause(page pagination.Query) string {
	column := page.SortColumn
	if column == "" {
		column = "repair.started_at"
	}
	if page.Descending {
		return column + " DESC, repair.id DESC"
	}
	return column + " ASC, repair.id ASC"
}

// parseHistoryQuery 解析履历查询参数: 故障类型(支持逗号分隔)与时间区间。
func parseHistoryQuery(query HistoryQuery) (historyFilter, pagination.Query, error) {
	page := pagination.Parse(query.Params, historySortSpec)
	filter := historyFilter{TimeColumn: "repair.started_at"}

	// 时间过滤列只允许开工/完工两个白名单值, 避免拼接用户输入。
	switch strings.TrimSpace(query.TimeField) {
	case "", "started_at":
		filter.TimeColumn = "repair.started_at"
	case "finished_at":
		filter.TimeColumn = "repair.finished_at"
		if page.SortColumn == "repair.started_at" {
			page.SortColumn = "repair.finished_at"
		}
	default:
		return filter, page, apperr.BadRequest("非法的时间维度: %s", query.TimeField)
	}

	if raw := strings.TrimSpace(query.FaultType); raw != "" {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				filter.FaultTypes = append(filter.FaultTypes, value)
			}
		}
	}

	if value := strings.TrimSpace(query.StartDate); value != "" {
		from, err := parseHistoryDay(value)
		if err != nil {
			return filter, page, err
		}
		filter.From = &from
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		to, err := parseHistoryDay(value)
		if err != nil {
			return filter, page, err
		}
		to = to.AddDate(0, 0, 1)
		filter.To = &to
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		return filter, page, apperr.BadRequest("结束日期不能早于开始日期")
	}
	return filter, page, nil
}

// parseHistoryDay 解析 YYYY-MM-DD 日期。
func parseHistoryDay(value string) (time.Time, error) {
	date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.Local)
	if err != nil {
		return time.Time{}, apperr.BadRequest("日期格式应为 YYYY-MM-DD, 当前值: %s", value)
	}
	return date, nil
}
