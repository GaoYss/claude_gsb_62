package bootstrap

import (
	"gorm.io/gorm"

	"streetlight/internal/module"
	"streetlight/internal/modules/fault"
	"streetlight/internal/modules/lamp"
	"streetlight/internal/modules/repair"
	"streetlight/internal/modules/settlement"
	"streetlight/internal/modules/status"
)

// buildModules 按依赖方向装配业务模块。
//
// 依赖关系: 路灯台账 <- 故障登记 <- 维修记录, 维修状态查询依赖三者的只读仓储;
// 费用结算直接引用维修数据表并为维修模块提供"是否已结算"端口。
// 其中两个反向依赖通过 setter 回填, 避免构造函数循环:
//   - 路灯删除前校验未闭环故障: lampService.SetOpenFaultCounter
//   - 维修删除前校验结算占用:   repairService.SetSettlementPort
func buildModules(db *gorm.DB) []module.Module {
	lampModule := lamp.New(db)

	faultModule := fault.New(db, lampModule.Service())
	lampModule.Service().SetOpenFaultCounter(faultModule.Repository())

	repairModule := repair.New(db, faultModule.Service())

	settlementModule := settlement.New(db)
	repairModule.Service().SetSettlementPort(settlementModule.Service())

	statusModule := status.New(
		db,
		lampModule.Repository(),
		faultModule.Repository(),
		repairModule.Repository(),
		settlementModule.Repository(),
	)

	return []module.Module{
		lampModule,
		faultModule,
		repairModule,
		settlementModule,
		statusModule,
	}
}
