package status_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"streetlight/internal/apperr"
	"streetlight/internal/modules/fault"
	"streetlight/internal/modules/repair"
	"streetlight/internal/modules/settlement"
	"streetlight/internal/modules/status"
	"streetlight/pkg/pagination"
)

// finishRepair 登记并完工一条维修记录, 时间可显式指定。
func (h *harness) finishRepair(t *testing.T, faultID uint, startedAt, finishedAt time.Time, cost float64) *repair.Repair {
	t.Helper()
	ctx := context.Background()
	entity, err := h.repairs.Create(ctx, repair.CreateRequest{
		FaultID:   faultID,
		Repairman: "维修工甲",
		RepairTeam: "市政照明一班",
		StartedAt: startedAt.Format("2006-01-02 15:04:05"),
	})
	require.NoError(t, err)
	c := cost
	record, err := h.repairs.Finish(ctx, entity.ID, repair.FinishRequest{
		Result:     repair.ResultFixed,
		FinishedAt: finishedAt.Format("2006-01-02 15:04:05"),
		Cost:       &c,
	})
	require.NoError(t, err)
	return record
}

// closedFaultWithRepair 在指定路灯上走一遍 登记故障 -> 维修 -> 完工 -> 关闭 的闭环。
func (h *harness) closedFaultWithRepair(t *testing.T, lampID uint, faultType string, reportedAt, startedAt, finishedAt time.Time, cost float64) *repair.Repair {
	t.Helper()
	ctx := context.Background()
	entity, err := h.faults.Create(ctx, fault.CreateRequest{
		LampID:      lampID,
		FaultType:   faultType,
		Description: "履历测试-" + faultType,
		ReportedAt:  reportedAt.Format("2006-01-02 15:04:05"),
	})
	require.NoError(t, err)
	record := h.finishRepair(t, entity.ID, startedAt, finishedAt, cost)
	_, err = h.faults.Close(ctx, entity.ID, fault.CloseRequest{})
	require.NoError(t, err)
	return record
}

func TestLampHistoryOrderingAndStats(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-001", "中山路")

	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.Local)
	// 两次闭环维修, 各耗时 2 小时 / 4 小时, 费用 100 / 300。
	h.closedFaultWithRepair(t, device.ID, "灯不亮", base, base.Add(time.Hour), base.Add(3*time.Hour), 100)
	h.closedFaultWithRepair(t, device.ID, "线路故障", base.AddDate(0, 0, 1), base.AddDate(0, 0, 1).Add(time.Hour), base.AddDate(0, 0, 1).Add(5*time.Hour), 300)

	result, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Total)
	require.Len(t, itemsOf(result), 2)

	// 默认按开工时间倒序: 线路故障(第二天)在前。
	require.Equal(t, "线路故障", result.Items[0].FaultType)
	require.Equal(t, "灯不亮", result.Items[1].FaultType)
	require.Equal(t, "市政照明一班", result.Items[0].RepairTeam)
	require.Equal(t, "维修工甲", result.Items[0].Repairman)

	// 累计统计。
	require.Equal(t, int64(2), result.Summary.LifetimeRepairCount)
	require.Equal(t, int64(2), result.Summary.LifetimeFinishedCount)
	require.Equal(t, int64(2), result.Summary.LifetimeFaultCount)
	require.InDelta(t, 3.0, result.Summary.LifetimeAverageDurationHr, 0.01)
	require.InDelta(t, 400.0, result.Summary.LifetimeTotalCost, 0.01)

	// 单条用时: 第一条 4 小时 = 240 分钟。
	require.NotNil(t, result.Items[0].DurationMinutes)
	require.Equal(t, int64(240), *result.Items[0].DurationMinutes)
}

func TestLampHistoryBackfillInsertedAtActualTime(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-002", "解放路")

	now := time.Now()
	recent := h.closedFaultWithRepair(t, device.ID, "灯不亮",
		now.Add(-48*time.Hour), now.Add(-47*time.Hour), now.Add(-46*time.Hour), 100)

	// 补录一条一年前的早期完工记录(故障也补录在一年前)。
	oldReported := now.AddDate(-1, 0, -2)
	oldStarted := now.AddDate(-1, 0, -1)
	oldFinished := now.AddDate(-1, 0, -1).Add(2 * time.Hour)
	oldFault, err := h.faults.Create(ctx, fault.CreateRequest{
		LampID:      device.ID,
		FaultType:   "灯杆倾斜",
		Description: "补录的历史故障",
		ReportedAt:  oldReported.Format("2006-01-02 15:04:05"),
	})
	require.NoError(t, err)
	backfilled, err := h.repairs.Create(ctx, repair.CreateRequest{
		FaultID:    oldFault.ID,
		Repairman:  "老周",
		RepairTeam: "市政照明二班",
		StartedAt:  oldStarted.Format("2006-01-02 15:04:05"),
		FinishedAt: oldFinished.Format("2006-01-02 15:04:05"),
		Result:     repair.ResultFixed,
		Cost:       floatPtr(800),
		Backfill:   true,
	})
	require.NoError(t, err)
	require.True(t, backfilled.Status == repair.StatusFinished)

	// 补录不改变新故障的闭环现状, 也不推进老故障状态机(仍为待处理)。
	got, err := h.faults.GetByID(ctx, oldFault.ID)
	require.NoError(t, err)
	require.Equal(t, fault.StatusPending, got.Status)
	require.Equal(t, 1, got.RepairCount)

	_ = recent
	result, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Total)
	// 倒序第一是最近的记录, 补录记录按实际开工时间落在第二, 而不是按录入 id 排第一。
	require.Equal(t, "灯不亮", result.Items[0].FaultType)
	require.Equal(t, "灯杆倾斜", result.Items[1].FaultType)
	require.True(t, result.Items[1].IsBackfilled)
}

func TestLampHistoryFilterByTypeAndRange(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-003", "滨江路")

	base := time.Date(2026, 5, 10, 9, 0, 0, 0, time.Local)
	h.closedFaultWithRepair(t, device.ID, "灯不亮", base, base.Add(time.Hour), base.Add(2*time.Hour), 100)
	h.closedFaultWithRepair(t, device.ID, "灯不亮", base.AddDate(0, 0, 5), base.AddDate(0, 0, 5).Add(time.Hour), base.AddDate(0, 0, 5).Add(2*time.Hour), 120)
	h.closedFaultWithRepair(t, device.ID, "线路故障", base.AddDate(0, 0, 10), base.AddDate(0, 0, 10).Add(time.Hour), base.AddDate(0, 0, 10).Add(2*time.Hour), 200)

	// 按故障类型收窄。
	byType, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{FaultType: "灯不亮"})
	require.NoError(t, err)
	require.Equal(t, int64(2), byType.Total)
	for _, item := range byType.Items {
		require.Equal(t, "灯不亮", item.FaultType)
	}
	// 顶部全量累计不随筛选波动。
	require.Equal(t, int64(3), byType.Summary.LifetimeRepairCount)
	require.Equal(t, int64(2), byType.Summary.RepairTotal)
	require.InDelta(t, 220.0, byType.Summary.TotalCost, 0.01)

	// 按时间段收窄(按开工时间)。
	byRange, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{
		StartDate: "2026-05-12",
		EndDate:   "2026-05-18",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), byRange.Total)
	require.Equal(t, "灯不亮", byRange.Items[0].FaultType)
}

func TestLampHistorySettlementReconcile(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-004", "学院路")

	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.Local)
	matched := h.closedFaultWithRepair(t, device.ID, "灯不亮", base, base.Add(time.Hour), base.Add(2*time.Hour), 100)
	different := h.closedFaultWithRepair(t, device.ID, "灯具破损", base.AddDate(0, 0, 1), base.AddDate(0, 0, 1).Add(time.Hour), base.AddDate(0, 0, 1).Add(2*time.Hour), 200)
	unbilled := h.closedFaultWithRepair(t, device.ID, "线路故障", base.AddDate(0, 0, 2), base.AddDate(0, 0, 2).Add(time.Hour), base.AddDate(0, 0, 2).Add(2*time.Hour), 300)
	_ = unbilled

	_, err := h.settlements.Create(ctx, settlement.CreateRequest{
		Title: "6月结算单",
		Items: []settlement.ItemRequest{
			{RepairID: matched.ID, Amount: 100},   // 与维修费一致
			{RepairID: different.ID, Amount: 230}, // 多结 30
		},
	})
	require.NoError(t, err)

	result, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(3), result.Total)
	require.Equal(t, int64(2), result.Summary.BilledCount)
	require.Equal(t, int64(1), result.Summary.MatchedCount)
	require.Equal(t, int64(1), result.Summary.DifferentCount)
	require.Equal(t, int64(1), result.Summary.UnbilledCount)
	require.InDelta(t, 330.0, result.Summary.BilledAmount, 0.01)

	byID := map[uint]status.HistoryItem{}
	for _, item := range result.Items {
		byID[item.RepairID] = item
	}
	require.Equal(t, settlement.ReconcileMatched, byID[matched.ID].Reconcile)
	require.Equal(t, settlement.ReconcileDifferent, byID[different.ID].Reconcile)
	require.Equal(t, 30.0, byID[different.ID].Difference)
	require.Equal(t, settlement.ReconcileUnbilled, byID[unbilled.ID].Reconcile)
	require.NotEmpty(t, byID[matched.ID].SettleNo)

	// 已结算的维修记录不允许删除。
	err = h.repairs.Delete(ctx, matched.ID)
	requireConflict(t, err)
}

// TestLampHistoryPaginationStableUnderLargeVolume 灌入单灯 3000 条维修记录,
// 校验深翻页不重不漏、统计准确且响应稳定。
func TestLampHistoryPaginationStableUnderLargeVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("大数据量用例在 -short 模式下跳过")
	}
	ctx := context.Background()
	h := newHarness(t)
	device := h.createLamp(t, "LD-H-BIG", "园区北路")

	const total = 3000
	faults := make([]fault.Fault, 0, total)
	repairs := make([]repair.Repair, 0, total)
	start := time.Date(2020, 1, 1, 8, 0, 0, 0, time.Local)

	for i := 0; i < total; i++ {
		reported := start.Add(time.Duration(i) * 24 * time.Hour)
		started := reported.Add(time.Hour)
		finished := started.Add(time.Duration(2+i%5) * time.Hour)
		faultNo := fmt.Sprintf("GDBULK%05d", i+1)
		repairNo := fmt.Sprintf("WXBULK%05d", i+1)
		faults = append(faults, fault.Fault{
			FaultNo: faultNo, LampID: device.ID, LampCode: device.Code, RoadName: device.RoadName,
			FaultType: []string{"灯不亮", "线路故障", "灯具破损"}[i%3],
			FaultLevel: fault.LevelNormal, Source: fault.SourceInspection,
			Description: "批量履历测试", Reporter: "测试员",
			ReportedAt: reported, Status: fault.StatusClosed, ClosedAt: &finished,
		})
		repairs = append(repairs, repair.Repair{
			RepairNo: repairNo, LampID: device.ID, LampCode: device.Code,
			Repairman: "批量维修工", RepairTeam: "市政照明一班",
			StartedAt: started, FinishedAt: &finished,
			Status: repair.StatusFinished, Result: repair.ResultFixed,
			Cost: float64(50 + i%100),
		})
	}
	require.NoError(t, h.db.CreateInBatches(&faults, 500).Error)
	for i := range repairs {
		repairs[i].FaultID = faults[i].ID
		repairs[i].FaultNo = faults[i].FaultNo
	}
	require.NoError(t, h.db.CreateInBatches(&repairs, 500).Error)

	// 统计走数据库聚合。
	first, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{
		Params: pageParams(1, 20),
	})
	require.NoError(t, err)
	require.Equal(t, int64(total), first.Total)
	require.Equal(t, int64(total), first.Summary.LifetimeRepairCount)
	require.Len(t, first.Items, 20)
	require.InDelta(t, 4.0, first.Summary.LifetimeAverageDurationHr, 0.01) // 平均 2..6 小时

	// 全量翻页, 校验不重不漏且严格倒序。
	seen := make(map[uint]struct{}, total)
	var previousStarted time.Time
	for page := 1; page <= 150; page++ {
		result, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{
			Params: pageParams(page, 20),
		})
		require.NoError(t, err)
		require.Len(t, result.Items, 20)
		for _, item := range result.Items {
			_, dup := seen[item.RepairID]
			require.False(t, dup, "第 %d 页出现重复维修记录 %d", page, item.RepairID)
			seen[item.RepairID] = struct{}{}
			if !previousStarted.IsZero() {
				require.True(t, !item.StartedAt.After(previousStarted),
					"第 %d 页排序错乱: %s 在 %s 之后", page, item.StartedAt, previousStarted)
			}
			previousStarted = item.StartedAt
		}
	}
	require.Len(t, seen, total)

	// 故障类型过滤后总数正确。
	filtered, err := h.status.LampHistory(ctx, device.ID, status.HistoryQuery{
		FaultType: "灯不亮",
		Params:    pageParams(1, 50),
	})
	require.NoError(t, err)
	require.Equal(t, int64(total/3), filtered.Total)
}

func itemsOf(result *status.LampHistory) []status.HistoryItem {
	return result.Items
}

func floatPtr(value float64) *float64 { return &value }

func pageParams(page, pageSize int) pagination.Params {
	return pagination.Params{Page: page, PageSize: pageSize}
}

// requireConflict 断言错误是 409 业务冲突。
func requireConflict(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	businessErr, ok := apperr.As(err)
	require.True(t, ok, "期望业务错误, 实际: %v", err)
	require.Equal(t, http.StatusConflict, businessErr.Status, "错误信息: %s", businessErr.Message)
}
