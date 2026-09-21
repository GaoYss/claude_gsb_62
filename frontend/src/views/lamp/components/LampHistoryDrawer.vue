<template>
  <el-drawer
    :model-value="modelValue"
    :title="lamp ? `${lamp.code} · 维修履历` : '维修履历'"
    size="92%"
    :destroy-on-close="true"
    @update:model-value="$emit('update:modelValue', $event)"
    @open="handleOpen"
  >
    <div v-loading="loading" class="history-drawer">
      <template v-if="lamp">
        <el-descriptions :column="4" border size="small" title="路灯档案">
          <el-descriptions-item label="路灯编号">{{ lamp.code }}</el-descriptions-item>
          <el-descriptions-item label="名称">{{ lamp.name || '-' }}</el-descriptions-item>
          <el-descriptions-item label="所在道路">{{ lamp.road_name }}</el-descriptions-item>
          <el-descriptions-item label="运行状态">
            <StatusTag :dict="RUN_STATUS" :value="lamp.run_status" />
          </el-descriptions-item>
          <el-descriptions-item label="区域">{{ lamp.district || '-' }}</el-descriptions-item>
          <el-descriptions-item label="灯具类型">{{ lamp.lamp_type || '-' }}</el-descriptions-item>
          <el-descriptions-item label="功率">{{ lamp.power ? `${lamp.power} W` : '-' }}</el-descriptions-item>
          <el-descriptions-item label="安装日期">{{ formatDate(lamp.install_date) }}</el-descriptions-item>
        </el-descriptions>

        <el-row :gutter="12" class="stat-row">
          <el-col :span="4">
            <StatCard label="累计维修次数" :value="summary.lifetime_repair_count" suffix="次" color="#409eff" icon="Tools" />
          </el-col>
          <el-col :span="5">
            <StatCard label="平均修复时长" :value="avgLifetime" hint="按已完工记录统计" color="#67c23a" icon="Timer" />
          </el-col>
          <el-col :span="4">
            <StatCard label="累计故障" :value="summary.lifetime_fault_count" suffix="次" color="#e6a23c" icon="Warning" />
          </el-col>
          <el-col :span="5">
            <StatCard label="累计维修费用" :value="money(summary.lifetime_total_cost)" color="#f56c6c" icon="Money" />
          </el-col>
          <el-col :span="6">
            <el-card shadow="hover" class="reconcile-card">
              <div class="text-muted reconcile-title">费用对账</div>
              <div class="reconcile-line">
                <el-tag size="small" type="success">一致 {{ summary.matched_count }}</el-tag>
                <el-tag size="small" type="danger">不一致 {{ summary.different_count }}</el-tag>
                <el-tag size="small" type="info">未结算 {{ summary.unbilled_count }}</el-tag>
              </div>
              <div class="text-muted reconcile-amount">
                结算金额合计 {{ money(summary.billed_amount) }} · 维修费合计 {{ money(summary.total_cost) }}
              </div>
            </el-card>
          </el-col>
        </el-row>

        <el-card shadow="never" class="filter-card">
          <div class="filter-bar">
            <el-select
              v-model="filters.fault_types"
              multiple
              collapse-tags
              collapse-tags-tooltip
              clearable
              placeholder="故障类型(可多选)"
              style="width: 260px"
            >
              <el-option v-for="item in faultTypes" :key="item" :label="item" :value="item" />
            </el-select>
            <el-date-picker
              v-model="filters.date_range"
              type="daterange"
              value-format="YYYY-MM-DD"
              range-separator="至"
              :start-placeholder="`${timeLabel}开始日期`"
              :end-placeholder="`${timeLabel}结束日期`"
              style="width: 280px"
            />
            <el-radio-group v-model="filters.time_field">
              <el-radio-button label="started_at">按开工时间</el-radio-button>
              <el-radio-button label="finished_at">按完工时间</el-radio-button>
            </el-radio-group>
            <el-button type="primary" :icon="Search" @click="search">查询</el-button>
            <el-button :icon="RefreshLeft" @click="reset">重置</el-button>
            <span class="filter-summary text-muted">
              当前筛选: {{ summary.repair_total }} 次维修 · 平均 {{ formatHours(summary.average_duration_hours) }} ·
              费用 {{ money(summary.total_cost) }}
            </span>
          </div>
        </el-card>

        <el-table v-loading="loading" :data="items" stripe size="small" class="history-table">
          <el-table-column type="expand">
            <template #default="{ row }">
              <div class="expand-box">
                <div><span class="text-muted">维修内容:</span> {{ row.content || '-' }}</div>
                <div><span class="text-muted">备注:</span> {{ row.remark || '-' }}</div>
                <div><span class="text-muted">联系电话:</span> {{ row.contact_phone || '-' }}</div>
                <div v-if="row.settle_no">
                  <span class="text-muted">结算单:</span> {{ row.settle_no }}
                  <StatusTag :dict="SETTLE_STATUS" :value="row.settle_status" />
                </div>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="开工时间" width="150">
            <template #default="{ row }">
              {{ formatDateTime(row.started_at) }}
              <el-tag v-if="row.is_backfilled" size="small" type="warning" effect="plain">补录</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="完工时间" width="150">
            <template #default="{ row }">{{ formatDateTime(row.finished_at) }}</template>
          </el-table-column>
          <el-table-column label="故障" width="180">
            <template #default="{ row }">
              <div>{{ row.fault_type }}</div>
              <div class="text-muted cell-sub">{{ row.fault_no }}</div>
            </template>
          </el-table-column>
          <el-table-column prop="repair_no" label="维修单号" width="140" />
          <el-table-column prop="repair_team" label="班组" width="120" />
          <el-table-column prop="repairman" label="人员" width="90" />
          <el-table-column label="更换灯具/耗材" min-width="160" show-overflow-tooltip>
            <template #default="{ row }">{{ row.materials || '-' }}</template>
          </el-table-column>
          <el-table-column label="用时" width="110">
            <template #default="{ row }">{{ formatDuration(row.duration_minutes) }}</template>
          </el-table-column>
          <el-table-column label="维修费" width="100" align="right">
            <template #default="{ row }">{{ money(row.cost) }}</template>
          </el-table-column>
          <el-table-column label="结算金额" width="110" align="right">
            <template #default="{ row }">
              <span v-if="row.settlement_id">{{ money(row.billed_amount) }}</span>
              <span v-else class="text-muted">-</span>
            </template>
          </el-table-column>
          <el-table-column label="对账" width="100">
            <template #default="{ row }">
              <el-tooltip
                v-if="row.reconcile === 'different'"
                :content="`差额 ${row.difference > 0 ? '+' : ''}${row.difference.toFixed(2)} 元`"
                placement="top"
              >
                <StatusTag :dict="RECONCILE_STATUS" :value="row.reconcile" />
              </el-tooltip>
              <StatusTag v-else :dict="RECONCILE_STATUS" :value="row.reconcile" />
            </template>
          </el-table-column>
          <el-table-column label="结果" width="90">
            <template #default="{ row }">
              <StatusTag v-if="row.result" :dict="REPAIR_RESULT" :value="row.result" />
              <StatusTag v-else :dict="REPAIR_STATUS" :value="row.status" />
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
      </template>
    </div>
  </el-drawer>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { RefreshLeft, Search } from '@element-plus/icons-vue'
import StatusTag from '@/components/common/StatusTag.vue'
import StatCard from '@/components/common/StatCard.vue'
import DataPagination from '@/components/common/DataPagination.vue'
import { statusApi } from '@/api/status'
import { useDictStore } from '@/stores/dict'
import { REPAIR_RESULT, REPAIR_STATUS, RECONCILE_STATUS, RUN_STATUS, SETTLE_STATUS } from '@/constants/dict'
import { formatDate, formatDateTime, formatDuration, formatHours, formatMoney } from '@/utils/format'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  lampId: { type: [Number, String], default: null },
})

defineEmits(['update:modelValue'])

const dictStore = useDictStore()
const loading = ref(false)
const lamp = ref(null)
const items = ref([])
const total = ref(0)
const summary = ref(emptySummary())

const query = reactive({ page: 1, page_size: 10 })
const filters = reactive({
  fault_types: [],
  date_range: null,
  time_field: 'started_at',
})

const faultTypes = computed(() => dictStore.faultMeta.fault_types ?? [])
const avgLifetime = computed(() => formatHours(summary.value.lifetime_average_duration_hours))
const timeLabel = computed(() => (filters.time_field === 'finished_at' ? '完工' : '开工'))

function emptySummary() {
  return {
    lifetime_repair_count: 0,
    lifetime_finished_count: 0,
    lifetime_fault_count: 0,
    lifetime_average_duration_hours: 0,
    lifetime_total_cost: 0,
    repair_total: 0,
    finished_total: 0,
    ongoing_total: 0,
    average_duration_hours: 0,
    total_cost: 0,
    billed_count: 0,
    matched_count: 0,
    different_count: 0,
    unbilled_count: 0,
    billed_amount: 0,
  }
}

function money(value) {
  return formatMoney(value)
}

async function handleOpen() {
  if (!props.lampId) return
  await dictStore.ensureLoaded().catch(() => {})
  reset()
}

function buildParams() {
  const params = {
    page: query.page,
    page_size: query.page_size,
    sort_by: filters.time_field,
    order: 'desc',
  }
  if (filters.fault_types.length) {
    params.fault_type = filters.fault_types.join(',')
  }
  if (filters.date_range && filters.date_range.length === 2) {
    ;[params.start_date, params.end_date] = filters.date_range
  }
  return params
}

async function load() {
  if (!props.lampId) return
  loading.value = true
  try {
    const data = await statusApi.lampHistory(props.lampId, buildParams())
    lamp.value = data.lamp
    items.value = data.items ?? []
    total.value = data.total ?? 0
    summary.value = { ...emptySummary(), ...(data.summary ?? {}) }
  } catch (error) {
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function search() {
  query.page = 1
  load()
}

function reset() {
  query.page = 1
  query.page_size = 10
  filters.fault_types = []
  filters.date_range = null
  filters.time_field = 'started_at'
  load()
}

function changePage(page) {
  query.page = page
  load()
}

function changePageSize(pageSize) {
  query.page = 1
  query.page_size = pageSize
  load()
}
</script>

<style scoped>
.history-drawer {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.stat-row {
  margin: 0;
}

.reconcile-card :deep(.el-card__body) {
  padding: 10px 14px;
}

.reconcile-title {
  font-size: 13px;
}

.reconcile-line {
  display: flex;
  gap: 6px;
  margin: 8px 0 6px;
  flex-wrap: wrap;
}

.reconcile-amount {
  font-size: 12px;
  line-height: 1.5;
}

.filter-card :deep(.el-card__body) {
  padding: 12px;
}

.filter-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.filter-summary {
  font-size: 12px;
}

.cell-sub {
  font-size: 12px;
}

.expand-box {
  padding: 8px 24px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
}
</style>
