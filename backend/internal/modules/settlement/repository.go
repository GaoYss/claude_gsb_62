package settlement

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"streetlight/internal/apperr"
	"streetlight/pkg/pagination"
)

// Repository 负责结算单与明细的数据访问。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造结算单仓储。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) session(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

// CreateWithUniqueNo 生成唯一结算单号并落库结算单头。
func (r *Repository) CreateWithUniqueNo(ctx context.Context, entity *Settlement, prefix string) error {
	for attempt := 0; attempt < 5; attempt++ {
		sequence, err := r.NextSequence(ctx, prefix)
		if err != nil {
			return err
		}
		entity.SettleNo = fmt.Sprintf("%s%04d", prefix, sequence+attempt)
		err = r.session(ctx).Create(entity).Error
		if err == nil {
			return nil
		}
		if !isUniqueViolation(err) {
			return fmt.Errorf("创建结算单失败: %w", err)
		}
	}
	return apperr.Conflict("结算单号生成冲突, 请稍后重试")
}

// NextSequence 返回指定前缀下可用的下一个流水号。
func (r *Repository) NextSequence(ctx context.Context, prefix string) (int, error) {
	var latest string
	err := r.session(ctx).Model(&Settlement{}).
		Where("settle_no LIKE ?", prefix+"%").
		Order("settle_no DESC").
		Limit(1).
		Pluck("settle_no", &latest).Error
	if err != nil {
		return 0, fmt.Errorf("生成结算单号失败: %w", err)
	}
	if latest == "" {
		return 1, nil
	}
	value, convErr := strconv.Atoi(strings.TrimPrefix(latest, prefix))
	if convErr != nil {
		return 1, nil
	}
	return value + 1, nil
}

// Update 保存结算单头全部字段。
func (r *Repository) Update(ctx context.Context, entity *Settlement) error {
	if err := r.session(ctx).Save(entity).Error; err != nil {
		return fmt.Errorf("更新结算单失败: %w", err)
	}
	return nil
}

// UpdateColumns 局部更新结算单字段。
func (r *Repository) UpdateColumns(ctx context.Context, id uint, columns map[string]any) error {
	if len(columns) == 0 {
		return nil
	}
	result := r.session(ctx).Model(&Settlement{}).Where("id = ?", id).Updates(columns)
	if result.Error != nil {
		return fmt.Errorf("更新结算单失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperr.NotFound("结算单不存在: id=%d", id)
	}
	return nil
}

// Delete 删除结算单及其明细。
func (r *Repository) Delete(ctx context.Context, id uint) error {
	err := r.session(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("settlement_id = ?", id).Delete(&SettlementItem{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&Settlement{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return apperr.NotFound("结算单不存在: id=%d", id)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("删除结算单失败: %w", err)
	}
	return nil
}

// GetByID 按主键查询结算单头。
func (r *Repository) GetByID(ctx context.Context, id uint) (*Settlement, error) {
	var entity Settlement
	err := r.session(ctx).First(&entity, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.NotFound("结算单不存在: id=%d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询结算单失败: %w", err)
	}
	return &entity, nil
}

// List 分页查询结算单。
func (r *Repository) List(ctx context.Context, filter Filter, page pagination.Query) ([]Settlement, int64, error) {
	base := func() *gorm.DB {
		return applyFilter(r.session(ctx).Model(&Settlement{}), filter)
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计结算单失败: %w", err)
	}

	entities := make([]Settlement, 0)
	if err := base().Order(page.OrderClause()).Offset(page.Offset()).Limit(page.Limit()).Find(&entities).Error; err != nil {
		return nil, 0, fmt.Errorf("查询结算单失败: %w", err)
	}
	return entities, total, nil
}

// ListItems 查询结算单明细。
func (r *Repository) ListItems(ctx context.Context, settlementID uint) ([]SettlementItem, error) {
	items := make([]SettlementItem, 0)
	err := r.session(ctx).
		Where("settlement_id = ?", settlementID).
		Order("id ASC").
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("查询结算单明细失败: %w", err)
	}
	return items, nil
}

// CreateItems 批量写入明细。
func (r *Repository) CreateItems(ctx context.Context, items []SettlementItem) error {
	if len(items) == 0 {
		return nil
	}
	if err := r.session(ctx).Create(&items).Error; err != nil {
		return fmt.Errorf("写入结算单明细失败: %w", err)
	}
	return nil
}

// DeleteItems 按主键删除明细。
func (r *Repository) DeleteItems(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.session(ctx).Where("id IN ?", ids).Delete(&SettlementItem{}).Error; err != nil {
		return fmt.Errorf("删除结算单明细失败: %w", err)
	}
	return nil
}

// CountBilledByRepairIDs 统计给定维修记录中已进入结算单的明细, 返回 repair_id -> 明细。
// repair_id 有唯一索引, 每笔维修至多命中一条。
func (r *Repository) ItemsByRepairIDs(ctx context.Context, repairIDs []uint) (map[uint]SettlementItem, error) {
	result := make(map[uint]SettlementItem)
	if len(repairIDs) == 0 {
		return result, nil
	}
	items := make([]SettlementItem, 0)
	err := r.session(ctx).
		Where("repair_id IN ?", repairIDs).
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("查询维修结算明细失败: %w", err)
	}
	for _, item := range items {
		result[item.RepairID] = item
	}
	return result, nil
}

// SettlementsByIDs 批量查询结算单头。
func (r *Repository) SettlementsByIDs(ctx context.Context, ids []uint) (map[uint]Settlement, error) {
	result := make(map[uint]Settlement)
	if len(ids) == 0 {
		return result, nil
	}
	entities := make([]Settlement, 0)
	if err := r.session(ctx).Where("id IN ?", ids).Find(&entities).Error; err != nil {
		return nil, fmt.Errorf("查询结算单失败: %w", err)
	}
	for _, entity := range entities {
		result[entity.ID] = entity
	}
	return result, nil
}

// ItemsByRepairID 返回某条维修记录对应的结算明细, 不存在时返回 nil。
func (r *Repository) ItemByRepairID(ctx context.Context, repairID uint) (*SettlementItem, error) {
	var item SettlementItem
	err := r.session(ctx).Where("repair_id = ?", repairID).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询维修结算明细失败: %w", err)
	}
	return &item, nil
}

// SumItemAmount 汇总某张结算单的明细金额与条数。
func (r *Repository) SumItemAmount(ctx context.Context, settlementID uint) (float64, int64, error) {
	type row struct {
		Total float64
		Count int64
	}
	var result row
	err := r.session(ctx).Model(&SettlementItem{}).
		Select("COALESCE(SUM(amount), 0) AS total, COUNT(*) AS count").
		Where("settlement_id = ?", settlementID).
		Scan(&result).Error
	if err != nil {
		return 0, 0, fmt.Errorf("汇总结算单金额失败: %w", err)
	}
	return result.Total, result.Count, nil
}

// Filter 是结算单列表查询条件。
type Filter struct {
	Status      string
	Keyword     string
	LampID      uint
	StartedFrom *time.Time
	StartedTo   *time.Time
}

// applyFilter 统一拼装结算单查询条件。
func applyFilter(statement *gorm.DB, filter Filter) *gorm.DB {
	if value := strings.TrimSpace(filter.Status); value != "" {
		statement = statement.Where("status = ?", value)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		statement = statement.Where(
			"settle_no LIKE ? OR title LIKE ? OR operator LIKE ?",
			like, like, like,
		)
	}
	if filter.LampID > 0 {
		statement = statement.Where(
			"EXISTS (SELECT 1 FROM settlement_item WHERE settlement_item.settlement_id = settlement.id AND settlement_item.lamp_id = ?)",
			filter.LampID,
		)
	}
	if filter.StartedFrom != nil {
		statement = statement.Where("created_at >= ?", *filter.StartedFrom)
	}
	if filter.StartedTo != nil {
		statement = statement.Where("created_at < ?", *filter.StartedTo)
	}
	return statement
}

// isUniqueViolation 兼容 sqlite 与 postgres 的唯一约束冲突判断。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique violation")
}
