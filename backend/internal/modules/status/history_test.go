package status_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"streetlight/internal/modules/fault"
	"streetlight/internal/modules/repair"
	"streetlight/internal/modules/settlement"
	"streetlight/internal/modules/status"
	"streetlight/pkg/pagination"
)

// finishRepair 登记一次维修并完工, 返回该维修记录。
func (h *harness) finishRepair(t *testing.T, faultID uint, startedAt, finishedAt time.Time, cost float64) *repair.Repair {
	t.Helper()
	ctx := context.Background()
	record, err := h.repairs.Create(ctx, repair.CreateRequest{
		FaultID:    faultID,
		Repairman:  "维修工甲",
		RepairTeam: "市政照明一班",
		StartedAt:  startedAt.Format("2006-01-02 15:04:05"),
		Cost:       nil,
	})
	require.NoError(t, err)
	c := cost
	_, err = h.repairs.Finish(ctx, record.ID, repair.FinishRequest{
		Result:     repair.ResultFixed,
		FinishedAt: finishedAt.Format("2006-01-02 15:04:05"),
		Cost:       &c,
	})
	require.NoError(t, err)
	return h.getRepair(t, record.ID)
}

func (h *harness) getRepair(t *testing.T, id uint) *repair.Repair {
	t.Helper()
	record, err := h.repairs.Get(context.Background(), id)
	require.NoError(t, err)
	return record
}

func TestRepairHistoryReconcileAndFilter(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-001", "中山路")

	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.Local)
	makeFaultRepair := func(faultType string, day int, cost float64) *repair.Repair {
		entity, err := h.faults.Create(ctx, fault.CreateRequest{
			LampID: device.ID, FaultType: faultType, FaultLevel: fault.LevelHigh,
			Description: "履历测试", Reporter: "巡检员",
			ReportedAt: base.AddDate(0, 0, day-1).Format("2006-01-02 15:04:05"),
		})
		require.NoError(t, err)
		started := base.AddDate(0, 0, day)
		record := h.finishRepair(t, entity.ID, started, started.Add(2*time.Hour), cost)
		_, err = h.faults.Close(ctx, entity.ID, fault.CloseRequest{})
		require.NoError(t, err)
		return record
	}

	r1 := makeFaultRepair("灯不亮", 1, 100)
	r2 := makeFaultRepair("线路故障", 5, 300)
	r3 := makeFaultRepair("灯不亮", 10, 200)

	// r1 进入已确认结算单且金额对平; r2 进入已确认结算单但金额不符; r3 进入草稿结算单。
	confirmed, err := h.settlements.Create(ctx, settlement.CreateRequest{Title: "3 月对账单"})
	require.NoError(t, err)
	_, err = h.settlements.AddItem(ctx, confirmed.ID, settlement.AddItemRequest{RepairID: r1.ID})
	require.NoError(t, err)
	mismatchAmount := 280.0
	_, err = h.settlements.AddItem(ctx, confirmed.ID, settlement.AddItemRequest{RepairID: r2.ID, Amount: &mismatchAmount})
	require.NoError(t, err)
	_, err = h.settlements.Confirm(ctx, confirmed.ID)
	require.NoError(t, err)

	draft, err := h.settlements.Create(ctx, settlement.CreateRequest{Title: "4 月草稿单"})
	require.NoError(t, err)
	_, err = h.settlements.AddItem(ctx, draft.ID, settlement.AddItemRequest{RepairID: r3.ID})
	require.NoError(t, err)

	page, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(3), page.Total)
	// 倒序: r3(第 10 天) -> r2(第 5 天) -> r1(第 1 天)
	require.Equal(t, r3.ID, page.Items[0].RepairID)
	require.Equal(t, r2.ID, page.Items[1].RepairID)
	require.Equal(t, r1.ID, page.Items[2].RepairID)

	summary := page.Summary
	require.Equal(t, int64(3), summary.TotalRepairs)
	require.Equal(t, int64(3), summary.FinishedCount)
	require.Equal(t, 600.0, summary.TotalCost)
	require.Equal(t, 2.0, summary.AverageDurationHr)
	require.Equal(t, int64(1), summary.MatchedCount)
	require.Equal(t, int64(1), summary.MismatchCount)
	require.Equal(t, int64(1), summary.SettlingCount)
	require.Equal(t, int64(0), summary.UnsettledCount)

	// 逐行对账字段。
	require.Equal(t, "settling", page.Items[0].ReconcileStatus)
	require.Equal(t, "mismatch", page.Items[1].ReconcileStatus)
	require.NotNil(t, page.Items[1].AmountDiff)
	require.Equal(t, -20.0, *page.Items[1].AmountDiff)
	require.Equal(t, "matched", page.Items[2].ReconcileStatus)

	// 按故障类型收窄。
	byType, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{FaultType: "灯不亮"})
	require.NoError(t, err)
	require.Equal(t, int64(2), byType.Total)
	require.Equal(t, int64(2), byType.Summary.TotalRepairs)
	require.Equal(t, int64(2), byType.Summary.MatchedCount+byType.Summary.SettlingCount)

	// 按时间段收窄。
	byRange, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{
		StartDate: "2026-03-07", EndDate: "2026-03-20",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), byRange.Total)
	require.Equal(t, r3.ID, byRange.Items[0].RepairID)

	// 按对账状态收窄。
	byMismatch, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{ReconcileStatus: "mismatch"})
	require.NoError(t, err)
	require.Equal(t, int64(1), byMismatch.Total)
	require.Equal(t, r2.ID, byMismatch.Items[0].RepairID)
}

func TestRepairHistoryBackfillOrdersByOccurredAt(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-002", "滨江路")

	base := time.Date(2026, 1, 10, 9, 0, 0, 0, time.Local)
	createFaultAt := func(at time.Time) uint {
		entity, err := h.faults.Create(ctx, fault.CreateRequest{
			LampID: device.ID, FaultType: "灯具常亮", FaultLevel: fault.LevelNormal,
			Description: "补录排序测试", Reporter: "巡检员",
			ReportedAt: at.Format("2006-01-02 15:04:05"),
		})
		require.NoError(t, err)
		return entity.ID
	}

	// 先录一条近期维修, 再补录一条更早的维修。
	recentFault := createFaultAt(base)
	recent := h.finishRepair(t, recentFault, base.Add(2*time.Hour), base.Add(3*time.Hour), 50)

	backfillFault := createFaultAt(base.AddDate(0, 0, -20))
	backfill := h.finishRepair(
		t, backfillFault,
		base.AddDate(0, 0, -20).Add(time.Hour),
		base.AddDate(0, 0, -20).Add(2*time.Hour),
		80,
	)
	// 补录记录录入时间更晚, 但开工时间更早。
	require.True(t, backfill.CreatedAt.After(recent.CreatedAt) || !backfill.CreatedAt.Before(recent.CreatedAt))

	page, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	// 倒序按实际发生时间: 近期记录在前, 补录的早期记录在后。
	require.Equal(t, recent.ID, page.Items[0].RepairID)
	require.Equal(t, backfill.ID, page.Items[1].RepairID)

	// 升序翻页时补录记录应位于第一条。
	asc, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{
		Params: pageParams(1, 10, "started_at", "asc"),
	})
	require.NoError(t, err)
	require.Equal(t, backfill.ID, asc.Items[0].RepairID)
}

func TestRepairHistoryPaginationStableAtScale(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-BIG", "园区北路")

	const total = 3000
	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.Local)

	// 批量直接造数据: 每条故障配一条已完工维修, 保证履历总量达几千条。
	entities := make([]fault.Fault, 0, total)
	for i := 0; i < total; i++ {
		reported := base.Add(time.Duration(i) * 50 * time.Minute)
		entities = append(entities, fault.Fault{
			FaultNo:     fmt.Sprintf("GDBIG%06d", i+1),
			LampID:      device.ID,
			LampCode:    device.Code,
			RoadName:    device.RoadName,
			FaultType:   []string{"灯不亮", "线路故障", "灯光闪烁"}[i%3],
			FaultLevel:  fault.LevelNormal,
			Source:      fault.SourceInspection,
			Description: "批量履历压测",
			Reporter:    "巡检员",
			ReportedAt:  reported,
			Status:      fault.StatusClosed,
		})
	}
	require.NoError(t, h.db.CreateInBatches(&entities, 50).Error)

	repairs := make([]repair.Repair, 0, total)
	for i := 0; i < total; i++ {
		started := entities[i].ReportedAt.Add(time.Hour)
		finished := started.Add(time.Duration(30+i%90) * time.Minute)
		repairs = append(repairs, repair.Repair{
			RepairNo:   fmt.Sprintf("WXBIG%06d", i+1),
			FaultID:    entities[i].ID,
			FaultNo:    entities[i].FaultNo,
			LampID:     device.ID,
			LampCode:   device.Code,
			Repairman:  "维修工甲",
			RepairTeam: "市政照明一班",
			StartedAt:  started,
			FinishedAt: &finished,
			Status:     repair.StatusFinished,
			Result:     repair.ResultFixed,
			Cost:       float64(100 + i%5*20),
		})
	}
	require.NoError(t, h.db.CreateInBatches(&repairs, 50).Error)

	const pageSize = 50
	first, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{
		Params: pageParams(1, pageSize, "started_at", "desc"),
	})
	require.NoError(t, err)
	require.Equal(t, int64(total), first.Total)
	require.Len(t, first.Items, pageSize)
	require.Equal(t, total/pageSize, first.TotalPages)
	// 最新一条(下标 total-1)应在第一页第一条。
	require.Equal(t, repairs[total-1].ID, first.Items[0].RepairID)

	// 连续翻页无重复、无遗漏。
	seen := make(map[uint]struct{}, total)
	for pageNo := 1; pageNo <= first.TotalPages; pageNo++ {
		p, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{
			Params: pageParams(pageNo, pageSize, "started_at", "desc"),
		})
		require.NoError(t, err)
		require.NotEmpty(t, p.Items)
		for _, item := range p.Items {
			_, dup := seen[item.RepairID]
			require.False(t, dup, "第 %d 页出现重复维修记录 id=%d", pageNo, item.RepairID)
			seen[item.RepairID] = struct{}{}
		}
	}
	require.Len(t, seen, total)

	// 最后一页边界。
	last, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{
		Params: pageParams(first.TotalPages, pageSize, "started_at", "desc"),
	})
	require.NoError(t, err)
	require.Equal(t, repairs[0].ID, last.Items[len(last.Items)-1].RepairID)

	// 统计口径与全量一致。
	require.Equal(t, int64(total), first.Summary.TotalRepairs)
	require.Equal(t, int64(total), first.Summary.FinishedCount)
	require.Equal(t, int64(0), first.Summary.OngoingCount)

	// 过滤后统计同口径: 某故障类型应为 1000 条。
	filtered, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{FaultType: "灯不亮"})
	require.NoError(t, err)
	require.Equal(t, int64(1000), filtered.Total)
	require.Equal(t, int64(1000), filtered.Summary.TotalRepairs)

	// 跳转到很深的页码也能稳定返回。
	deep, err := h.status.RepairHistory(ctx, device.ID, status.RepairHistoryQuery{
		Params: pageParams(59, pageSize, "started_at", "desc"),
	})
	require.NoError(t, err)
	require.Len(t, deep.Items, pageSize)
}

func pageParams(page, pageSize int, sortBy, order string) pagination.Params {
	return pagination.Params{Page: page, PageSize: pageSize, SortBy: sortBy, Order: order}
}

func TestSettlementItemRejectsDuplicateRepair(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-003", "解放路")
	base := time.Date(2026, 5, 1, 8, 0, 0, 0, time.Local)
	entity, err := h.faults.Create(ctx, fault.CreateRequest{
		LampID: device.ID, FaultType: "灯不亮", FaultLevel: fault.LevelNormal,
		Description: "唯一结算约束", Reporter: "巡检员",
		ReportedAt: base.Format("2006-01-02 15:04:05"),
	})
	require.NoError(t, err)
	record := h.finishRepair(t, entity.ID, base.Add(time.Hour), base.Add(2*time.Hour), 120)

	first, err := h.settlements.Create(ctx, settlement.CreateRequest{Title: "结算单 A"})
	require.NoError(t, err)
	_, err = h.settlements.AddItem(ctx, first.ID, settlement.AddItemRequest{RepairID: record.ID})
	require.NoError(t, err)

	second, err := h.settlements.Create(ctx, settlement.CreateRequest{Title: "结算单 B"})
	require.NoError(t, err)
	_, err = h.settlements.AddItem(ctx, second.ID, settlement.AddItemRequest{RepairID: record.ID})
	require.Error(t, err)

	// 已确认的结算单不允许再改明细。
	_, err = h.settlements.Confirm(ctx, first.ID)
	require.NoError(t, err)
	_, err = h.settlements.AddItem(ctx, first.ID, settlement.AddItemRequest{RepairID: record.ID})
	require.Error(t, err)
}
