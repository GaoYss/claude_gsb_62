package status_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRepairHistoryUsesLampStartedIndex 保证单灯履历的倒序翻页命中
// (lamp_id, started_at) 复合索引, 数据量增长后翻页仍稳定。
func TestRepairHistoryUsesLampStartedIndex(t *testing.T) {
	h := newHarness(t)
	device := h.createLamp(t, "LD-PLAN-1", "中山路")

	rows, err := h.db.Raw("EXPLAIN QUERY PLAN "+
		"SELECT r.id FROM repair AS r "+
		"LEFT JOIN fault AS f ON f.id = r.fault_id "+
		"LEFT JOIN settlement_item AS si ON si.repair_id = r.id "+
		"LEFT JOIN settlement AS s ON s.id = si.settlement_id "+
		"WHERE r.lamp_id = ? ORDER BY r.started_at DESC, r.id DESC LIMIT 10 OFFSET 200", device.ID).Rows()
	require.NoError(t, err)
	defer rows.Close()

	usedIndex := false
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		t.Logf("plan: %s", detail)
		if strings.Contains(detail, "idx_repair_lamp_started") {
			usedIndex = true
		}
	}
	require.True(t, usedIndex, "维修履历查询应命中 idx_repair_lamp_started 复合索引")
}
