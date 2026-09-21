package settlement

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Module 结算单模块: 管理维修费用结算单与逐条明细, 供维修履历对账。
type Module struct {
	repository *Repository
	service    *Service
	handler    *Handler
}

// New 构造结算单模块。
func New(db *gorm.DB) *Module {
	repository := NewRepository(db)
	service := NewService(db, repository)
	return &Module{
		repository: repository,
		service:    service,
		handler:    NewHandler(service),
	}
}

// Repository 暴露仓储。
func (m *Module) Repository() *Repository { return m.repository }

// Service 暴露服务, 供其它模块装配对账端口。
func (m *Module) Service() *Service { return m.service }

// Name 实现 module.Module 接口。
func (m *Module) Name() string { return "费用结算" }

// Models 实现 module.Module 接口。
func (m *Module) Models() []any { return []any{&Settlement{}, &SettlementItem{}} }

// RegisterRoutes 实现 module.Module 接口。
func (m *Module) RegisterRoutes(api *gin.RouterGroup) {
	group := api.Group("/settlements")
	{
		group.GET("", m.handler.List)
		group.POST("", m.handler.Create)
		group.GET("/repair-candidates", m.handler.Candidates)
		group.GET("/:id", m.handler.Get)
		group.PUT("/:id", m.handler.Update)
		group.DELETE("/:id", m.handler.Delete)
		group.POST("/:id/confirm", m.handler.Confirm)
		group.POST("/:id/items", m.handler.AddItem)
		group.DELETE("/:id/items/:itemId", m.handler.RemoveItem)
	}
}
