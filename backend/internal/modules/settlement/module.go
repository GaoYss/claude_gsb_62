package settlement

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Module 结算单模块, 记录每次维修费用的结算去向, 供维修履历逐条对账。
type Module struct {
	repository *Repository
	service    *Service
	handler    *Handler
}

// New 构造结算单模块, repairs 为维修模块暴露的查询端口。
func New(db *gorm.DB, repairs RepairPort) *Module {
	repository := NewRepository(db)
	service := NewService(repository, repairs)
	return &Module{
		repository: repository,
		service:    service,
		handler:    NewHandler(service),
	}
}

// Repository 暴露仓储, 供状态查询模块装配。
func (m *Module) Repository() *Repository { return m.repository }

// Service 暴露服务, 供测试或其它模块复用。
func (m *Module) Service() *Service { return m.service }

// Name 实现 module.Module 接口。
func (m *Module) Name() string { return "结算单" }

// Models 实现 module.Module 接口。
func (m *Module) Models() []any { return []any{&Settlement{}, &SettlementItem{}} }

// RegisterRoutes 实现 module.Module 接口。
func (m *Module) RegisterRoutes(api *gin.RouterGroup) {
	group := api.Group("/settlements")
	{
		group.GET("", m.handler.List)
		group.POST("", m.handler.Create)
		group.GET("/:id", m.handler.Get)
		group.DELETE("/:id", m.handler.Delete)
		group.POST("/:id/items", m.handler.AddItem)
		group.DELETE("/:id/items/:itemId", m.handler.RemoveItem)
		group.POST("/:id/confirm", m.handler.Confirm)
	}
}
