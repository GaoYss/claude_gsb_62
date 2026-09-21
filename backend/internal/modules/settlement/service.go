package settlement

import (
	"context"
	"strings"
	"time"

	"streetlight/internal/apperr"
	"streetlight/internal/modules/repair"
	"streetlight/pkg/pagination"
)

// settlementSortSpec 定义结算单列表允许的排序字段白名单。
var settlementSortSpec = pagination.SortSpec{
	Allowed: map[string]string{
		"settle_no":    "settle_no",
		"status":       "status",
		"total_amount": "total_amount",
		"created_at":   "created_at",
		"confirmed_at": "confirmed_at",
	},
	Default: "created_at",
}

// RepairPort 由维修记录模块实现, 结算模块通过它读取维修费用与归属。
type RepairPort interface {
	GetByID(ctx context.Context, id uint) (*repair.Repair, error)
}

// Service 承载结算单业务规则。
type Service struct {
	repo    *Repository
	repairs RepairPort
}

// NewService 构造结算单服务。
func NewService(repo *Repository, repairs RepairPort) *Service {
	return &Service{repo: repo, repairs: repairs}
}

// Create 新建结算单。
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Settlement, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, apperr.BadRequest("结算单标题不能为空")
	}
	periodStart, err := parseDay(req.PeriodStart)
	if err != nil {
		return nil, err
	}
	periodEnd, err := parseDay(req.PeriodEnd)
	if err != nil {
		return nil, err
	}
	if periodStart != nil && periodEnd != nil && periodEnd.Before(*periodStart) {
		return nil, apperr.BadRequest("结算周期结束日期不能早于开始日期")
	}

	now := time.Now()
	entity := &Settlement{
		Title:       title,
		RepairTeam:  strings.TrimSpace(req.RepairTeam),
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Status:      StatusDraft,
		Remark:      strings.TrimSpace(req.Remark),
	}
	if err := s.repo.CreateWithUniqueNo(ctx, entity, "JS"+now.Format("20060102")); err != nil {
		return nil, err
	}
	return entity, nil
}

// List 分页查询结算单。
func (s *Service) List(ctx context.Context, query ListQuery) ([]Settlement, int64, pagination.Query, error) {
	page := pagination.Parse(query.Params, settlementSortSpec)
	filter := Filter{
		Keyword:    strings.TrimSpace(query.Keyword),
		Status:     strings.TrimSpace(query.Status),
		RepairTeam: strings.TrimSpace(query.RepairTeam),
	}
	if filter.Status != "" && !IsValidStatus(filter.Status) {
		return nil, 0, page, apperr.BadRequest("非法的结算单状态: %s", filter.Status)
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		from, err := parseDay(value)
		if err != nil {
			return nil, 0, page, err
		}
		filter.CreatedFrom = from
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		to, err := parseDay(value)
		if err != nil {
			return nil, 0, page, err
		}
		endExclusive := to.AddDate(0, 0, 1)
		filter.CreatedTo = &endExclusive
	}
	items, total, err := s.repo.List(ctx, filter, page)
	if err != nil {
		return nil, 0, page, err
	}
	return items, total, page, nil
}

// Get 查询结算单详情(含明细)。
func (s *Service) Get(ctx context.Context, id uint) (*Detail, error) {
	entity, items, err := s.repo.GetWithItems(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Detail{Settlement: entity, Items: items}, nil
}

// AddItem 向草稿结算单追加一条维修费用明细。
func (s *Service) AddItem(ctx context.Context, id uint, req AddItemRequest) (*Detail, error) {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if entity.Status != StatusDraft {
		return nil, apperr.Conflict("结算单 %s 已确认, 不允许再追加明细", entity.SettleNo)
	}

	record, err := s.repairs.GetByID(ctx, req.RepairID)
	if err != nil {
		return nil, err
	}
	if record.Status != repair.StatusFinished {
		return nil, apperr.Conflict("维修记录 %s 尚未完工, 不能进入结算", record.RepairNo)
	}

	amount := record.Cost
	if req.Amount != nil {
		amount = *req.Amount
	}

	item := &SettlementItem{
		SettlementID: entity.ID,
		RepairID:     record.ID,
		RepairNo:     record.RepairNo,
		LampID:       record.LampID,
		LampCode:     record.LampCode,
		FaultNo:      record.FaultNo,
		Amount:       amount,
		Remark:       strings.TrimSpace(req.Remark),
	}
	if err := s.repo.CreateItem(ctx, item); err != nil {
		return nil, err
	}
	if err := s.refreshTotal(ctx, entity.ID); err != nil {
		return nil, err
	}
	return s.Get(ctx, entity.ID)
}

// RemoveItem 从草稿结算单移除一条明细。
func (s *Service) RemoveItem(ctx context.Context, id, itemID uint) error {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if entity.Status != StatusDraft {
		return apperr.Conflict("结算单 %s 已确认, 不允许删除明细", entity.SettleNo)
	}
	if err := s.repo.DeleteItem(ctx, id, itemID); err != nil {
		return err
	}
	return s.refreshTotal(ctx, id)
}

// Confirm 确认结算单: 锁定金额, 之后作为履历费用对账基准。
func (s *Service) Confirm(ctx context.Context, id uint) (*Settlement, error) {
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if entity.Status == StatusConfirmed {
		return nil, apperr.Conflict("结算单 %s 已确认, 无需重复操作", entity.SettleNo)
	}
	count, err := s.repo.CountItems(ctx, id)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, apperr.Conflict("结算单 %s 尚无明细, 不能确认", entity.SettleNo)
	}
	now := time.Now()
	entity.Status = StatusConfirmed
	entity.ConfirmedAt = &now
	if err := s.repo.Update(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

// Delete 删除结算单, 仅草稿状态允许删除。
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

// refreshTotal 按明细金额重算结算单总额。
func (s *Service) refreshTotal(ctx context.Context, id uint) error {
	total, err := s.repo.SumItems(ctx, id)
	if err != nil {
		return err
	}
	entity, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	entity.TotalAmount = total
	return s.repo.Update(ctx, entity)
}

// parseDay 解析 YYYY-MM-DD 日期, 空串返回 nil。
func parseDay(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	date, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return nil, apperr.BadRequest("日期格式应为 YYYY-MM-DD, 当前值: %s", value)
	}
	return &date, nil
}
