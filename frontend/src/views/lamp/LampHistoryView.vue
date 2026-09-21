<template>
  <div class="page" v-loading="loading">
    <PageHeader :title="`维修履历 · ${lamp.code || ''}`" :description="lampSubtitle">
      <el-button :icon="Back" @click="goBack">返回台账</el-button>
      <el-button :icon="Refresh" @click="load">刷新</el-button>
    </PageHeader>

    <el-card shadow="never" class="lamp-card">
      <el-descriptions :column="4" border size="small">
        <el-descriptions-item label="路灯编号">{{ lamp.code || '-' }}</el-descriptions-item>
        <el-descriptions-item label="名称">{{ lamp.name || '-' }}</el-descriptions-item>
        <el-descriptions-item label="所在道路">{{ lamp.road_name || '-' }}</el-descriptions-item>
        <el-descriptions-item label="区域">{{ lamp.district || '-' }}</el-descriptions-item>
        <el-descriptions-item label="灯具类型">{{ lamp.lamp_type || '-' }}</el-descriptions-item>
        <el-descriptions-item label="功率">{{ lamp.power ? `${lamp.power} W` : '-' }}</el-descriptions-item>
        <el-descriptions-item label="运行状态">
          <StatusTag :dict="RUN_STATUS" :value="lamp.run_status" />
        </el-descriptions-item>
        <el-descriptions-item label="安装日期">{{ formatDate(lamp.install_date) }}</el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-row :gutter="12" class="stat-row">
      <el-col :span="6">
        <StatCard label="累计维修次数" :value="summary.total_repairs" suffix="次" hint="当前筛选口径" icon="Tools" color="#409eff" />
      </el-col>
      <el-col :span="6">
        <StatCard label="平均修复时长" :value="Number(summary.average_duration_hr || 0).toFixed(1)" suffix="小时" hint="仅统计已完工记录" icon="Timer" color="#67c23a" />
      </el-col>
      <el-col :span="6">
        <StatCard label="维修费用合计" :value="formatMoney(summary.total_cost)" hint="当前筛选口径" icon="Money" color="#e6a23c" />
      </el-col>
      <el-col :span="6">
        <StatCard
          label="对账情况"
          :value="`${summary.matched_count} / ${summary.finished_count}`"
          suffix="已对平"
          :hint="reconcileHint"
          icon="CircleCheck"
          :color="summary.mismatch_count > 0 ? '#f56c6c' : '#67c23a'"
        />
      </el-col>
    </el-row>

    <el-card shadow="never">
      <div class="filter-bar">
        <el-select v-model="query.fault_type" placeholder="故障类型" clearable style="width: 150px">
          <el-option v-for="item in dictStore.faultMeta.fault_types" :key="item" :label="item" :value="item" />
        </el-select>
        <el-select v-model="query.reconcile_status" placeholder="对账状态" clearable style="width: 140px">
          <el-option v-for="(item, key) in RECONCILE_STATUS" :key="key" :label="item.label" :value="key" />
        </el-select>
        <el-date-picker
          v-model="dateRange"
          type="daterange"
          value-format="YYYY-MM-DD"
          range-separator="至"
          start-placeholder="开工开始日期"
          end-placeholder="开工结束日期"
        />
        <el-button type="primary" :icon="Search" @click="handleSearch">查询</el-button>
        <el-button :icon="RefreshLeft" @click="handleReset">重置</el-button>
      </div>
    </el-card>

    <el-card shadow="never">
      <el-table :data="items" stripe @sort-change="handleSortChange">
        <el-table-column label="开工时间" prop="started_at" width="150" sortable="custom">
          <template #default="{ row }">
            <div>{{ formatDateTime(row.started_at) }}</div>
            <div v-if="isBackfilled(row)" class="text-muted backfill-hint">补录 · {{ formatDate(row.created_at) }}</div>
          </template>
        </el-table-column>
        <el-table-column label="完工时间" width="150" sortable="custom" prop="finished_at">
          <template #default="{ row }">{{ formatDateTime(row.finished_at) }}</template>
        </el-table-column>
        <el-table-column label="用时" width="110">
          <template #default="{ row }">{{ formatDuration(row.duration_minutes) }}</template>
        </el-table-column>
        <el-table-column prop="fault_no" label="故障单号" width="140" />
        <el-table-column prop="fault_type" label="故障类型" width="100" />
        <el-table-column label="等级" width="80">
          <template #default="{ row }"><StatusTag :dict="FAULT_LEVEL" :value="row.fault_level" /></template>
        </el-table-column>
        <el-table-column prop="repair_team" label="班组" width="120" show-overflow-tooltip />
        <el-table-column prop="repairman" label="维修人员" width="90" />
        <el-table-column prop="materials" label="更换灯具 / 耗材" min-width="160" show-overflow-tooltip />
        <el-table-column label="结果" width="90">
          <template #default="{ row }">
            <StatusTag v-if="row.result" :dict="REPAIR_RESULT" :value="row.result" />
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="维修费用" width="100" sortable="custom" prop="cost">
          <template #default="{ row }">{{ formatMoney(row.cost) }}</template>
        </el-table-column>
        <el-table-column label="结算单 / 结算金额" width="180">
          <template #default="{ row }">
            <template v-if="row.settle_no">
              <el-link type="primary" @click="openSettlement(row)">{{ row.settle_no }}</el-link>
              <div>{{ formatMoney(row.settled_amount) }}</div>
            </template>
            <span v-else class="text-muted">未结算</span>
          </template>
        </el-table-column>
        <el-table-column label="差额" width="90">
          <template #default="{ row }">
            <span v-if="row.amount_diff !== null && row.amount_diff !== undefined" :class="diffClass(row.amount_diff)">
              {{ row.amount_diff > 0 ? '+' : '' }}{{ Number(row.amount_diff).toFixed(2) }}
            </span>
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="对账状态" width="100" fixed="right">
          <template #default="{ row }"><StatusTag :dict="RECONCILE_STATUS" :value="row.reconcile_status" /></template>
        </el-table-column>
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="row.status === 'finished' && (row.reconcile_status === 'unsettled')"
              link type="primary" @click="openSettleEntry(row)"
            >登记结算</el-button>
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
      </el-table>

      <DataPagination
        :page="query.page"
        :page-size="query.page_size"
        :total="total"
        @page-change="changePage"
        @size-change="changePageSize"
      />
    </el-card>

    <SettleEntryDialog v-model="settleVisible" :repair="settlingRow" @saved="load" />
    <SettlementDetailDrawer v-model="settlementVisible" :settlement-id="activeSettlementId" @changed="load" />
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Back, Refresh, RefreshLeft, Search } from '@element-plus/icons-vue'
import PageHeader from '@/components/common/PageHeader.vue'
import StatusTag from '@/components/common/StatusTag.vue'
import StatCard from '@/components/common/StatCard.vue'
import DataPagination from '@/components/common/DataPagination.vue'
import SettleEntryDialog from './components/SettleEntryDialog.vue'
import SettlementDetailDrawer from './components/SettlementDetailDrawer.vue'
import { statusApi } from '@/api/status'
import { useDictStore } from '@/stores/dict'
import { FAULT_LEVEL, REPAIR_RESULT, RECONCILE_STATUS, RUN_STATUS } from '@/constants/dict'
import { formatDate, formatDateTime, formatDuration, formatMoney } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const dictStore = useDictStore()

const lampId = Number(route.params.id)
const loading = ref(false)
const lamp = ref({})
const items = ref([])
const total = ref(0)
const summary = ref({})
const dateRange = ref([])
const settleVisible = ref(false)
const settlementVisible = ref(false)
const settlingRow = ref(null)
const activeSettlementId = ref(null)

const query = reactive({
  page: 1,
  page_size: 10,
  fault_type: '',
  reconcile_status: '',
  start_date: '',
  end_date: '',
  sort_by: 'started_at',
  order: 'desc',
})

const lampSubtitle = computed(() => {
  const parts = [lamp.value.road_name, lamp.value.lamp_type].filter(Boolean)
  return `该路灯历次故障与维修的完整履历, 按实际发生时间倒序排列${parts.length ? ` · ${parts.join(' · ')}` : ''}`
})

const reconcileHint = computed(() => {
  const s = summary.value
  if (!s) return ''
  return `不符 ${s.mismatch_count ?? 0} · 结算中 ${s.settling_count ?? 0} · 未结算 ${s.unsettled_count ?? 0}`
})

async function load() {
  if (!lampId) return
  loading.value = true
  try {
    const data = await statusApi.repairHistory(lampId, { ...query })
    lamp.value = data.lamp || {}
    items.value = data.items || []
    total.value = data.total || 0
    summary.value = data.summary || {}
  } catch (error) {
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  query.start_date = dateRange.value?.[0] ?? ''
  query.end_date = dateRange.value?.[1] ?? ''
  query.page = 1
  load()
}

function handleReset() {
  Object.assign(query, {
    page: 1, page_size: 10, fault_type: '', reconcile_status: '',
    start_date: '', end_date: '', sort_by: 'started_at', order: 'desc',
  })
  dateRange.value = []
  load()
}

function changePage(page) {
  query.page = page
  load()
}

function changePageSize(size) {
  query.page = 1
  query.page_size = size
  load()
}

function handleSortChange({ prop, order }) {
  query.sort_by = prop || 'started_at'
  query.order = order === 'ascending' ? 'asc' : 'desc'
  load()
}

function isBackfilled(row) {
  // 录入时间晚于开工时间一天以上, 视为补录记录。
  return new Date(row.created_at).getTime() - new Date(row.started_at).getTime() > 24 * 3600 * 1000
}

function diffClass(diff) {
  if (Math.abs(diff) < 0.005) return 'diff-ok'
  return 'diff-bad'
}

function openSettleEntry(row) {
  settlingRow.value = row
  settleVisible.value = true
}

function openSettlement(row) {
  activeSettlementId.value = row.settlement_id
  settlementVisible.value = true
}

function goBack() {
  router.push('/lamps')
}

onMounted(() => {
  dictStore.ensureLoaded().catch(() => {})
  load()
})
</script>

<style scoped>
.lamp-card {
  margin-top: 12px;
}

.stat-row {
  margin-top: 12px;
}

.stat-row .el-col {
  margin-bottom: 4px;
}

.backfill-hint {
  font-size: 12px;
}

.diff-ok {
  color: var(--el-color-success);
}

.diff-bad {
  color: var(--el-color-danger);
  font-weight: 600;
}
</style>
