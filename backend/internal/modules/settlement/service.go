package settlement

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"

	"streetlight/internal/apperr"
	"streetlight/internal/modules/fault"
	"streetlight/internal/modules/repair"
	"streetlight/pkg/pagination"
)

// costTolerance 是金额对账的容差, 规避浮点尾差。
const costTolerance = 0.005

// CostTolerance 导出对账容差, 供履历等只读模块复用同一判定口径。
const CostTolerance = costTolerance

// ReconcileState 依据维修费用与结算金额计算逐条对账结果。
// billed 为 false 表示该笔维修尚未进入结算单。
func ReconcileState(repairCost, billedAmount float64, billed bool) string {
	if !billed {
		return ReconcileUnbilled
	}
	if math.Abs(billedAmount-repairCost) <= CostTolerance {
		return ReconcileMatched
	}
	return ReconcileDifferent
}

// settlementSortSpec 定义结算单列表允许的排序字段白名单。
var settlementSortSpec = pagination.SortSpec{
	Allowed: map[string]string{
		"settle_no":    "settle_no",
		"created_at":   "created_at",
		"confirmed_at": "confirmed_at",
		"status":       "status",
		"total_amount": "total_amount",
	},
	Default: "created_at",
}

// Service 承载结算单的业务规则。
// 结算明细与维修记录是一对一关系, 登记金额时冗余维修侧的费用/灯具等快照,
// 履历对账以维修记录现值为准, 差额在查询时实时计算。
type Service struct {
	db   *gorm.DB
	repo *Repository
}

// NewService 构造结算单服务。
func NewService(db *gorm.DB, repo *Repository) *Service {
	return &Service{db: db, repo: repo}
}

// Create 新建结算单, 可一并写入初始明细。
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Detail, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, apperr.BadRequest("结算单标题不能为空")
	}
	periodStart, periodEnd, err := parsePeriod(req.PeriodStart, req.PeriodEnd)
	if err != nil {
		return nil, err
	}
	if err := validateItems(req.Items); err != nil {
		return nil, err
	}

	now := time.Now()
	entity := &Settlement{
		Title:       title,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Status:      StatusDraft,
		Operator:    strings.TrimSpace(req.Operator),
		Remark:      strings.TrimSpace(req.Remark),
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := NewRepository(tx)
		if err := txRepo.CreateWithUniqueNo(ctx, entity, "JS"+now.Format("20060102")); err != nil {
			return err
		}
		if len(req.Items) > 0 {
			items, err := s.buildItems(tx, ctx, entity.ID, req.Items)
			if err != nil {
				return err
			}
			if err := txRepo.CreateItems(ctx, items); err != nil {
				return mapSettleError(err)
			}
		}
		return s.refreshTotal(tx, ctx, entity.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, entity.ID)
}

// Update 修改草稿结算单; items 非空时按提交内容全量替换明细。
func (s *Service) Update(ctx context.Context, id uint, req UpdateRequest) (*Detail, error) {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if entity.Status != StatusDraft {
		return nil, apperr.Conflict("结算单 %s 已确认, 不允许修改", entity.SettleNo)
	}

	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, apperr.BadRequest("结算单标题不能为空")
		}
		entity.Title = title
	}
	if req.Operator != nil {
		entity.Operator = strings.TrimSpace(*req.Operator)
	}
	if req.Remark != nil {
		entity.Remark = strings.TrimSpace(*req.Remark)
	}
	if req.PeriodStart != nil || req.PeriodEnd != nil {
		start := ""
		end := ""
		if entity.PeriodStart != nil {
			start = entity.PeriodStart.Format("2006-01-02")
		}
		if entity.PeriodEnd != nil {
			end = entity.PeriodEnd.Format("2006-01-02")
		}
		if req.PeriodStart != nil {
			start = strings.TrimSpace(*req.PeriodStart)
		}
		if req.PeriodEnd != nil {
			end = strings.TrimSpace(*req.PeriodEnd)
		}
		periodStart, periodEnd, err := parsePeriod(start, end)
		if err != nil {
			return nil, err
		}
		entity.PeriodStart = periodStart
		entity.PeriodEnd = periodEnd
	}
	if req.Items != nil {
		if err := validateItems(*req.Items); err != nil {
			return nil, err
		}
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := NewRepository(tx)
		if err := txRepo.Update(ctx, entity); err != nil {
			return err
		}
		if req.Items != nil {
			if err := tx.Where("settlement_id = ?", id).Delete(&SettlementItem{}).Error; err != nil {
				return fmt.Errorf("清空结算明细失败: %w", err)
			}
			if len(*req.Items) > 0 {
				items, err := s.buildItems(tx, ctx, id, *req.Items)
				if err != nil {
					return err
				}
				if err := txRepo.CreateItems(ctx, items); err != nil {
					return mapSettleError(err)
				}
			}
		}
		return s.refreshTotal(tx, ctx, id)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// AddItem 向草稿结算单追加一条维修费用明细。
func (s *Service) AddItem(ctx context.Context, id uint, req ItemRequest) (*Detail, error) {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if entity.Status != StatusDraft {
		return nil, apperr.Conflict("结算单 %s 已确认, 不允许追加明细", entity.SettleNo)
	}
	if err := validateItems([]ItemRequest{req}); err != nil {
		return nil, err
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := NewRepository(tx)
		items, err := s.buildItems(tx, ctx, id, []ItemRequest{req})
		if err != nil {
			return err
		}
		if err := txRepo.CreateItems(ctx, items); err != nil {
			return mapSettleError(err)
		}
		return s.refreshTotal(tx, ctx, id)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// RemoveItem 从草稿结算单删除一条明细。
func (s *Service) RemoveItem(ctx context.Context, id uint, itemID uint) (*Detail, error) {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if entity.Status != StatusDraft {
		return nil, apperr.Conflict("结算单 %s 已确认, 不允许删除明细", entity.SettleNo)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND settlement_id = ?", itemID, id).Delete(&SettlementItem{})
		if result.Error != nil {
			return fmt.Errorf("删除结算明细失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return apperr.NotFound("结算明细不存在: id=%d", itemID)
		}
		return s.refreshTotal(tx, ctx, id)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Confirm 确认结算单, 确认后不可再修改。
func (s *Service) Confirm(ctx context.Context, id uint, req ConfirmRequest) (*Detail, error) {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if entity.Status == StatusConfirmed {
		return nil, apperr.Conflict("结算单 %s 已确认, 请勿重复操作", entity.SettleNo)
	}
	_, itemCount, err := s.repo.SumItemAmount(ctx, id)
	if err != nil {
		return nil, err
	}
	if itemCount == 0 {
		return nil, apperr.Conflict("结算单 %s 尚无明细, 不允许确认", entity.SettleNo)
	}
	now := time.Now()
	columns := map[string]any{
		"status":       StatusConfirmed,
		"confirmed_at": &now,
	}
	if strings.TrimSpace(req.Remark) != "" {
		columns["remark"] = strings.TrimSpace(req.Remark)
	}
	if err := s.repo.UpdateColumns(ctx, id, columns); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除结算单(连带明细)。
func (s *Service) Delete(ctx context.Context, id uint) error {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if entity.Status == StatusConfirmed {
		return apperr.Conflict("结算单 %s 已确认, 不允许删除", entity.SettleNo)
	}
	return s.repo.Delete(ctx, id)
}

// Get 查询结算单详情, 并补齐维修侧信息与逐条对账结果。
func (s *Service) Get(ctx context.Context, id uint) (*Detail, error) {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListItems(ctx, id)
	if err != nil {
		return nil, err
	}
	views, err := s.decorateItems(ctx, items)
	if err != nil {
		return nil, err
	}
	return &Detail{Settlement: *entity, Items: views}, nil
}

// List 分页查询结算单。
func (s *Service) List(ctx context.Context, query ListQuery) ([]Settlement, int64, pagination.Query, error) {
	page := pagination.Parse(query.Params, settlementSortSpec)
	filter := Filter{
		Status:  strings.TrimSpace(query.Status),
		Keyword: strings.TrimSpace(query.Keyword),
		LampID:  query.LampID,
	}
	if filter.Status != "" && !IsValidStatus(filter.Status) {
		return nil, 0, page, apperr.BadRequest("非法的结算单状态: %s", filter.Status)
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		from, err := parseDay(value)
		if err != nil {
			return nil, 0, page, err
		}
		filter.StartedFrom = &from
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		to, err := parseDay(value)
		if err != nil {
			return nil, 0, page, err
		}
		to = to.AddDate(0, 0, 1)
		filter.StartedTo = &to
	}
	items, total, err := s.repo.List(ctx, filter, page)
	if err != nil {
		return nil, 0, page, err
	}
	return items, total, page, nil
}

// IsRepairSettled 判断维修记录是否已进入结算单, 返回结算单 ID 与单号; 实现维修模块的删除校验端口。
func (s *Service) IsRepairSettled(ctx context.Context, repairID uint) (bool, uint, string, error) {
	item, err := s.repo.ItemByRepairID(ctx, repairID)
	if err != nil {
		return false, 0, "", err
	}
	if item == nil {
		return false, 0, "", nil
	}
	header, err := s.repo.GetByID(ctx, item.SettlementID)
	if err != nil {
		return true, item.SettlementID, "", nil
	}
	return true, item.SettlementID, header.SettleNo, nil
}

// buildItems 依据维修记录批量构造明细, 校验记录存在且未被其它结算单占用。
func (s *Service) buildItems(tx *gorm.DB, ctx context.Context, settlementID uint, requests []ItemRequest) ([]SettlementItem, error) {
	repairIDs := make([]uint, 0, len(requests))
	seen := make(map[uint]struct{}, len(requests))
	amounts := make(map[uint]float64, len(requests))
	remarks := make(map[uint]string, len(requests))
	for _, item := range requests {
		if _, dup := seen[item.RepairID]; dup {
			return nil, apperr.BadRequest("同一次提交中维修记录 %d 出现了多次", item.RepairID)
		}
		seen[item.RepairID] = struct{}{}
		repairIDs = append(repairIDs, item.RepairID)
		amounts[item.RepairID] = item.Amount
		remarks[item.RepairID] = strings.TrimSpace(item.Remark)
	}

	records := make([]repair.Repair, 0, len(repairIDs))
	if err := tx.WithContext(ctx).Where("id IN ?", repairIDs).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("查询维修记录失败: %w", err)
	}
	if len(records) != len(repairIDs) {
		existing := make(map[uint]struct{}, len(records))
		for _, record := range records {
			existing[record.ID] = struct{}{}
		}
		for _, id := range repairIDs {
			if _, ok := existing[id]; !ok {
				return nil, apperr.NotFound("维修记录不存在: id=%d", id)
			}
		}
	}

	// 已被其它结算单占用的维修记录直接拒绝(同一单内的重复在全量替换场景允许, 因旧明细已清空)。
	occupied := make([]SettlementItem, 0)
	if err := tx.WithContext(ctx).
		Where("repair_id IN ? AND settlement_id <> ?", repairIDs, settlementID).
		Find(&occupied).Error; err != nil {
		return nil, fmt.Errorf("校验结算占用失败: %w", err)
	}
	if len(occupied) > 0 {
		var header Settlement
		if err := tx.WithContext(ctx).First(&header, occupied[0].SettlementID).Error; err == nil {
			return nil, apperr.Conflict("维修记录 %s 已在结算单 %s 中, 不能重复结算", occupied[0].RepairNo, header.SettleNo)
		}
		return nil, apperr.Conflict("维修记录 %s 已在其它结算单中, 不能重复结算", occupied[0].RepairNo)
	}

	items := make([]SettlementItem, 0, len(records))
	for _, record := range records {
		items = append(items, SettlementItem{
			SettlementID: settlementID,
			RepairID:     record.ID,
			RepairNo:     record.RepairNo,
			FaultID:      record.FaultID,
			FaultNo:      record.FaultNo,
			LampID:       record.LampID,
			LampCode:     record.LampCode,
			Amount:       amounts[record.ID],
			RepairCost:   record.Cost,
			Remark:       remarks[record.ID],
		})
	}
	return items, nil
}

// decorateItems 用维修记录现值补齐明细展示字段并实时计算对账结果。
func (s *Service) decorateItems(ctx context.Context, items []SettlementItem) ([]ItemView, error) {
	if len(items) == 0 {
		return make([]ItemView, 0), nil
	}
	repairIDs := make([]uint, 0, len(items))
	for _, item := range items {
		repairIDs = append(repairIDs, item.RepairID)
	}
	records := make([]repair.Repair, 0, len(repairIDs))
	if err := s.db.WithContext(ctx).Where("id IN ?", repairIDs).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("查询维修记录失败: %w", err)
	}
	recordMap := make(map[uint]repair.Repair, len(records))
	faultIDs := make([]uint, 0, len(records))
	for _, record := range records {
		recordMap[record.ID] = record
		faultIDs = append(faultIDs, record.FaultID)
	}
	faults := make([]fault.Fault, 0, len(faultIDs))
	if err := s.db.WithContext(ctx).Where("id IN ?", faultIDs).Find(&faults).Error; err != nil {
		return nil, fmt.Errorf("查询故障记录失败: %w", err)
	}
	faultMap := make(map[uint]fault.Fault, len(faults))
	for _, item := range faults {
		faultMap[item.ID] = item
	}

	views := make([]ItemView, 0, len(items))
	for _, item := range items {
		view := ItemView{SettlementItem: item}
		record, ok := recordMap[item.RepairID]
		if ok {
			if entity, exists := faultMap[record.FaultID]; exists {
				view.FaultType = entity.FaultType
			}
			view.RepairTeam = record.RepairTeam
			view.Repairman = record.Repairman
			startedAt := record.StartedAt
			view.StartedAt = &startedAt
			diff := item.Amount - record.Cost
			view.Difference = round2(diff)
			if math.Abs(diff) <= costTolerance {
				view.Reconcile = ReconcileMatched
			} else {
				view.Reconcile = ReconcileDifferent
			}
		}
		views = append(views, view)
	}
	return views, nil
}

// refreshTotal 依据明细重算结算单头的金额合计与条数。
func (s *Service) refreshTotal(tx *gorm.DB, ctx context.Context, settlementID uint) error {
	txRepo := NewRepository(tx)
	total, count, err := txRepo.SumItemAmount(ctx, settlementID)
	if err != nil {
		return err
	}
	return txRepo.UpdateColumns(ctx, settlementID, map[string]any{
		"total_amount": round2(total),
		"item_count":   count,
	})
}

// validateItems 校验明细请求体。
func validateItems(items []ItemRequest) error {
	for _, item := range items {
		if item.RepairID == 0 {
			return apperr.BadRequest("明细必须指定维修记录")
		}
		if item.Amount < 0 {
			return apperr.BadRequest("结算金额不能为负数")
		}
	}
	return nil
}

// mapSettleError 将唯一约束冲突转换为友好的业务冲突提示。
func mapSettleError(err error) error {
	if isUniqueViolation(err) {
		return apperr.Conflict("存在已在其它结算单中登记的维修记录, 不能重复结算")
	}
	return err
}

// parsePeriod 解析结算周期起止日期并校验先后顺序。
func parsePeriod(start, end string) (*time.Time, *time.Time, error) {
	var periodStart *time.Time
	var periodEnd *time.Time
	if value := strings.TrimSpace(start); value != "" {
		date, err := parseDay(value)
		if err != nil {
			return nil, nil, err
		}
		periodStart = &date
	}
	if value := strings.TrimSpace(end); value != "" {
		date, err := parseDay(value)
		if err != nil {
			return nil, nil, err
		}
		periodEnd = &date
	}
	if periodStart != nil && periodEnd != nil && periodEnd.Before(*periodStart) {
		return nil, nil, apperr.BadRequest("结算周期结束日期不能早于开始日期")
	}
	return periodStart, periodEnd, nil
}

// parseDay 解析 YYYY-MM-DD 日期, 返回当天零点。
func parseDay(value string) (time.Time, error) {
	date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.Local)
	if err != nil {
		return time.Time{}, apperr.BadRequest("日期格式应为 YYYY-MM-DD, 当前值: %s", value)
	}
	return date, nil
}

// round2 保留两位小数。
func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
