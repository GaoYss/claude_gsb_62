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

// Filter 是仓储层使用的结算单查询条件。
type Filter struct {
	Keyword     string
	Status      string
	RepairTeam  string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
}

// Repository 负责结算单的数据访问。
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

// Create 新建结算单。
func (r *Repository) Create(ctx context.Context, entity *Settlement) error {
	if err := r.session(ctx).Create(entity).Error; err != nil {
		return fmt.Errorf("新建结算单失败: %w", err)
	}
	return nil
}

// CreateWithUniqueNo 生成唯一结算单号并落库, 冲突时自动重试。
func (r *Repository) CreateWithUniqueNo(ctx context.Context, entity *Settlement, prefix string) error {
	for attempt := 0; attempt < 5; attempt++ {
		sequence, err := r.NextSequence(ctx, prefix)
		if err != nil {
			return err
		}
		entity.SettleNo = fmt.Sprintf("%s%04d", prefix, sequence+attempt)
		err = r.Create(ctx, entity)
		if err == nil {
			return nil
		}
		if !isUniqueViolation(err) {
			return err
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

// Update 保存结算单全部字段。
func (r *Repository) Update(ctx context.Context, entity *Settlement) error {
	if err := r.session(ctx).Save(entity).Error; err != nil {
		return fmt.Errorf("更新结算单失败: %w", err)
	}
	return nil
}

// Delete 按主键删除结算单(连同明细)。
func (r *Repository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("settlement_id = ?", id).Delete(&SettlementItem{}).Error; err != nil {
			return fmt.Errorf("删除结算单明细失败: %w", err)
		}
		if err := tx.Delete(&Settlement{}, id).Error; err != nil {
			return fmt.Errorf("删除结算单失败: %w", err)
		}
		return nil
	})
}

// GetByID 按主键查询结算单。
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

// GetWithItems 查询结算单及其全部明细。
func (r *Repository) GetWithItems(ctx context.Context, id uint) (*Settlement, []SettlementItem, error) {
	entity, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	items := make([]SettlementItem, 0)
	if err := r.session(ctx).Where("settlement_id = ?", id).
		Order("id ASC").Find(&items).Error; err != nil {
		return nil, nil, fmt.Errorf("查询结算单明细失败: %w", err)
	}
	return entity, items, nil
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
		return nil, 0, fmt.Errorf("查询结算单列表失败: %w", err)
	}
	return entities, total, nil
}

// CreateItem 新增一条结算明细。
func (r *Repository) CreateItem(ctx context.Context, item *SettlementItem) error {
	if err := r.session(ctx).Create(item).Error; err != nil {
		if isUniqueViolation(err) {
			return apperr.Conflict("维修记录 %s 已存在结算明细, 一条维修记录只能结算一次", item.RepairNo)
		}
		return fmt.Errorf("新增结算明细失败: %w", err)
	}
	return nil
}

// DeleteItem 删除一条结算明细。
func (r *Repository) DeleteItem(ctx context.Context, settlementID, itemID uint) error {
	result := r.session(ctx).
		Where("id = ? AND settlement_id = ?", itemID, settlementID).
		Delete(&SettlementItem{})
	if result.Error != nil {
		return fmt.Errorf("删除结算明细失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperr.NotFound("结算明细不存在: id=%d", itemID)
	}
	return nil
}

// GetItem 按主键查询明细。
func (r *Repository) GetItem(ctx context.Context, itemID uint) (*SettlementItem, error) {
	var item SettlementItem
	err := r.session(ctx).First(&item, itemID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.NotFound("结算明细不存在: id=%d", itemID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询结算明细失败: %w", err)
	}
	return &item, nil
}

// SumItems 汇总某张结算单的明细金额。
func (r *Repository) SumItems(ctx context.Context, settlementID uint) (float64, error) {
	var total float64
	err := r.session(ctx).Model(&SettlementItem{}).
		Where("settlement_id = ?", settlementID).
		Select("COALESCE(SUM(amount), 0)").Scan(&total).Error
	if err != nil {
		return 0, fmt.Errorf("汇总结算金额失败: %w", err)
	}
	return total, nil
}

// CountItems 统计某张结算单的明细条数。
func (r *Repository) CountItems(ctx context.Context, settlementID uint) (int64, error) {
	var total int64
	err := r.session(ctx).Model(&SettlementItem{}).
		Where("settlement_id = ?", settlementID).Count(&total).Error
	if err != nil {
		return 0, fmt.Errorf("统计结算明细失败: %w", err)
	}
	return total, nil
}

// DistinctTeams 返回已使用过的班组名称。
func (r *Repository) DistinctTeams(ctx context.Context) ([]string, error) {
	values := make([]string, 0)
	err := r.session(ctx).Model(&Settlement{}).
		Where("repair_team <> ''").
		Distinct().Order("repair_team").
		Pluck("repair_team", &values).Error
	if err != nil {
		return nil, fmt.Errorf("查询结算班组选项失败: %w", err)
	}
	return values, nil
}

// applyFilter 统一拼装结算单查询条件。
func applyFilter(statement *gorm.DB, filter Filter) *gorm.DB {
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		statement = statement.Where("settle_no LIKE ? OR title LIKE ?", like, like)
	}
	if value := strings.TrimSpace(filter.Status); value != "" {
		statement = statement.Where("status = ?", value)
	}
	if value := strings.TrimSpace(filter.RepairTeam); value != "" {
		statement = statement.Where("repair_team = ?", value)
	}
	if filter.CreatedFrom != nil {
		statement = statement.Where("created_at >= ?", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		statement = statement.Where("created_at < ?", *filter.CreatedTo)
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
