package settlement_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"streetlight/internal/apperr"
	"streetlight/internal/modules/fault"
	"streetlight/internal/modules/lamp"
	"streetlight/internal/modules/repair"
	"streetlight/internal/modules/settlement"
)

var lampSeq uint64

type harness struct {
	db          *gorm.DB
	lamps       *lamp.Service
	faults      *fault.Service
	repairs     *repair.Service
	settlements *settlement.Service
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&lamp.Lamp{}, &fault.Fault{}, &repair.Repair{},
		&settlement.Settlement{}, &settlement.SettlementItem{},
	))

	lampRepo := lamp.NewRepository(db)
	lampService := lamp.NewService(lampRepo)
	faultRepo := fault.NewRepository(db)
	faultService := fault.NewService(faultRepo, lampService)
	lampService.SetOpenFaultCounter(faultRepo)
	repairRepo := repair.NewRepository(db)
	repairService := repair.NewService(repairRepo, faultService)
	settlementRepo := settlement.NewRepository(db)
	settlementService := settlement.NewService(db, settlementRepo)
	repairService.SetSettlementPort(settlementService)

	return &harness{
		db: db, lamps: lampService, faults: faultService,
		repairs: repairService, settlements: settlementService,
	}
}

func (h *harness) finishedRepair(t *testing.T, cost float64) *repair.Repair {
	t.Helper()
	ctx := context.Background()
	device, err := h.lamps.Create(ctx, lamp.CreateRequest{
		Code:     "LD-T-" + randomSuffix(),
		RoadName: "测试路",
		LampType: lamp.LampTypeLED,
	})
	require.NoError(t, err)
	entity, err := h.faults.Create(ctx, fault.CreateRequest{
		LampID: device.ID, FaultType: "灯不亮", Description: "结算测试",
	})
	require.NoError(t, err)
	record, err := h.repairs.Create(ctx, repair.CreateRequest{FaultID: entity.ID, Repairman: "维修工甲"})
	require.NoError(t, err)
	c := cost
	record, err = h.repairs.Finish(ctx, record.ID, repair.FinishRequest{Result: repair.ResultFixed, Cost: &c})
	require.NoError(t, err)
	return record
}

func randomSuffix() string {
	seq := atomic.AddUint64(&lampSeq, 1)
	return time.Now().Format("150405") + "-" + strconv.FormatUint(seq, 10)
}

func TestSettlementCreateAndLineByLineReconcile(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	first := h.finishedRepair(t, 100)
	second := h.finishedRepair(t, 200)

	detail, err := h.settlements.Create(ctx, settlement.CreateRequest{
		Title: "2026年6月维修结算",
		Items: []settlement.ItemRequest{
			{RepairID: first.ID, Amount: 100},
			{RepairID: second.ID, Amount: 220},
		},
	})
	require.NoError(t, err)
	require.Equal(t, settlement.StatusDraft, detail.Status)
	require.Len(t, detail.Items, 2)
	require.InDelta(t, 320, detail.TotalAmount, 0.01)
	require.Equal(t, 2, detail.ItemCount)
	require.Regexp(t, `^JS\d{8}\d{4}$`, detail.SettleNo)

	itemByRepair := map[uint]settlement.ItemView{}
	for _, item := range detail.Items {
		itemByRepair[item.RepairID] = item
	}
	require.Equal(t, settlement.ReconcileMatched, itemByRepair[first.ID].Reconcile)
	require.Equal(t, settlement.ReconcileDifferent, itemByRepair[second.ID].Reconcile)
	require.InDelta(t, 20, itemByRepair[second.ID].Difference, 0.01)
}

func TestSettlementRepairCannotBeSettledTwice(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	record := h.finishedRepair(t, 100)

	_, err := h.settlements.Create(ctx, settlement.CreateRequest{
		Title: "结算单 A",
		Items: []settlement.ItemRequest{{RepairID: record.ID, Amount: 100}},
	})
	require.NoError(t, err)

	_, err = h.settlements.Create(ctx, settlement.CreateRequest{
		Title: "结算单 B",
		Items: []settlement.ItemRequest{{RepairID: record.ID, Amount: 100}},
	})
	require.Error(t, err)
	businessErr, ok := apperr.As(err)
	require.True(t, ok)
	require.Equal(t, 409, businessErr.Status)
}

func TestSettlementConfirmFreezesAndBlocksDelete(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	record := h.finishedRepair(t, 100)

	detail, err := h.settlements.Create(ctx, settlement.CreateRequest{
		Title: "待确认",
		Items: []settlement.ItemRequest{{RepairID: record.ID, Amount: 100}},
	})
	require.NoError(t, err)

	confirmed, err := h.settlements.Confirm(ctx, detail.ID, settlement.ConfirmRequest{})
	require.NoError(t, err)
	require.Equal(t, settlement.StatusConfirmed, confirmed.Status)
	require.NotNil(t, confirmed.ConfirmedAt)

	// 已确认不可改、不可删。
	_, err = h.settlements.Update(ctx, detail.ID, settlement.UpdateRequest{Title: strPtr("新标题")})
	require.Error(t, err)
	err = h.settlements.Delete(ctx, detail.ID)
	require.Error(t, err)
}

func TestSettlementEmptyCannotConfirm(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	detail, err := h.settlements.Create(ctx, settlement.CreateRequest{Title: "空结算单"})
	require.NoError(t, err)
	_, err = h.settlements.Confirm(ctx, detail.ID, settlement.ConfirmRequest{})
	require.Error(t, err)
}

func strPtr(value string) *string { return &value }
