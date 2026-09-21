package settlement

import (
	"context"
	"strings"

	"streetlight/internal/modules/repair"
	"streetlight/pkg/pagination"
)

// candidateSortSpec 候选维修记录的排序白名单, 默认完工时间倒序。
var candidateSortSpec = pagination.SortSpec{
	Allowed: map[string]string{
		"finished_at": "repair.finished_at",
		"started_at":  "repair.started_at",
		"cost":        "repair.cost",
	},
	Default: "repair.finished_at",
}

// ListCandidates 分页查询"已完工且尚未结算"的维修记录, 供新建结算单挑选明细。
// 已完工才产生确定费用; 已存在结算明细的记录通过 NOT EXISTS 排除。
func (s *Service) ListCandidates(ctx context.Context, query CandidateQuery) ([]CandidateRepair, int64, pagination.Query, error) {
	page := pagination.Parse(query.Params, candidateSortSpec)

	statement := s.db.WithContext(ctx).
		Model(&repair.Repair{}).
		Joins("JOIN fault ON fault.id = repair.fault_id").
		Where("repair.status = ?", repair.StatusFinished).
		Where("NOT EXISTS (SELECT 1 FROM settlement_item WHERE settlement_item.repair_id = repair.id)")

	if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		statement = statement.Where(
			"repair.repair_no LIKE ? OR repair.fault_no LIKE ? OR repair.lamp_code LIKE ? OR repair.repairman LIKE ?",
			like, like, like, like,
		)
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		from, err := parseDay(value)
		if err != nil {
			return nil, 0, page, err
		}
		statement = statement.Where("repair.finished_at >= ?", from)
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		to, err := parseDay(value)
		if err != nil {
			return nil, 0, page, err
		}
		statement = statement.Where("repair.finished_at < ?", to.AddDate(0, 0, 1))
	}

	var total int64
	if err := statement.Count(&total).Error; err != nil {
		return nil, 0, page, err
	}

	type row struct {
		repair.Repair
		FaultType string
		RoadName  string
	}
	rows := make([]row, 0, page.PageSize)
	if err := statement.
		Select("repair.*, fault.fault_type AS fault_type, fault.road_name AS road_name").
		Order(candidateOrderClause(page)).
		Offset(page.Offset()).
		Limit(page.Limit()).
		Scan(&rows).Error; err != nil {
		return nil, 0, page, err
	}

	items := make([]CandidateRepair, 0, len(rows))
	for _, item := range rows {
		items = append(items, CandidateRepair{
			RepairID:   item.ID,
			RepairNo:   item.RepairNo,
			FaultID:    item.FaultID,
			FaultNo:    item.FaultNo,
			FaultType:  item.FaultType,
			LampID:     item.LampID,
			LampCode:   item.LampCode,
			RoadName:   item.RoadName,
			RepairTeam: item.RepairTeam,
			Repairman:  item.Repairman,
			StartedAt:  item.StartedAt,
			FinishedAt: item.FinishedAt,
			Materials:  item.Materials,
			Cost:       item.Cost,
		})
	}
	return items, total, page, nil
}

// candidateOrderClause 返回候选列表排序片段, JOIN 后列名全限定并以 repair.id 兜底。
func candidateOrderClause(page pagination.Query) string {
	column := page.SortColumn
	if column == "" {
		column = "repair.finished_at"
	}
	if page.Descending {
		return column + " DESC, repair.id DESC"
	}
	return column + " ASC, repair.id ASC"
}
