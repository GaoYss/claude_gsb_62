<template>
  <el-drawer
    :model-value="modelValue"
    title="结算单详情与对账"
    size="70%"
    :destroy-on-close="true"
    @update:model-value="$emit('update:modelValue', $event)"
    @open="load"
  >
    <div v-loading="loading">
      <template v-if="detail.id">
        <el-descriptions :column="3" border size="small" title="结算单">
          <el-descriptions-item label="结算单号">{{ detail.settle_no }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <StatusTag :dict="SETTLE_STATUS" :value="detail.status" />
          </el-descriptions-item>
          <el-descriptions-item label="经办人">{{ detail.operator || '-' }}</el-descriptions-item>
          <el-descriptions-item label="标题" :span="3">{{ detail.title }}</el-descriptions-item>
          <el-descriptions-item label="结算周期">
            {{ formatDate(detail.period_start) }} 至 {{ formatDate(detail.period_end) }}
          </el-descriptions-item>
          <el-descriptions-item label="确认时间">{{ formatDateTime(detail.confirmed_at) }}</el-descriptions-item>
          <el-descriptions-item label="金额合计">
            <span class="amount-strong">{{ formatMoney(detail.total_amount) }}</span>
            <span class="text-muted">（{{ detail.item_count }} 笔）</span>
          </el-descriptions-item>
          <el-descriptions-item v-if="detail.remark" label="备注" :span="3">{{ detail.remark }}</el-descriptions-item>
        </el-descriptions>

        <div class="reconcile-summary">
          <el-tag type="success">金额一致 {{ matchedCount }}</el-tag>
          <el-tag type="danger">金额不一致 {{ differentCount }}</el-tag>
          <el-tag type="info">明细 {{ detail.items.length }} 笔</el-tag>
          <span class="text-muted">对账以维修记录当前费用为准, 差额 = 结算金额 - 维修费</span>
        </div>

        <el-table :data="detail.items" size="small" border>
          <el-table-column prop="repair_no" label="维修单号" width="140" />
          <el-table-column prop="lamp_code" label="路灯编号" width="100" />
          <el-table-column prop="fault_type" label="故障类型" width="90" />
          <el-table-column prop="repair_team" label="班组" width="110" />
          <el-table-column prop="repairman" label="人员" width="80" />
          <el-table-column label="开工时间" width="140">
            <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
          </el-table-column>
          <el-table-column label="维修费" width="100" align="right">
            <template #default="{ row }">{{ formatMoney(row.repair_cost) }}</template>
          </el-table-column>
          <el-table-column label="结算金额" width="110" align="right">
            <template #default="{ row }">{{ formatMoney(row.amount) }}</template>
          </el-table-column>
          <el-table-column label="差额" width="100" align="right">
            <template #default="{ row }">
              <span :class="row.difference !== 0 ? 'diff-bad' : 'diff-ok'">
                {{ row.difference > 0 ? '+' : '' }}{{ row.difference.toFixed(2) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="对账" width="100">
            <template #default="{ row }">
              <StatusTag :dict="RECONCILE_STATUS" :value="row.reconcile" />
            </template>
          </el-table-column>
          <el-table-column v-if="detail.status === 'draft'" label="操作" width="70">
            <template #default="{ row }">
              <el-button link type="danger" @click="removeItem(row)">移除</el-button>
            </template>
          </el-table-column>
        </el-table>

        <div v-if="detail.status === 'draft'" class="footer-actions">
          <el-button type="primary" @click="confirmSettlement">确认结算单</el-button>
          <span class="text-muted">确认后结算单冻结, 不可再修改或删除</span>
        </div>
      </template>
    </div>
  </el-drawer>
</template>

<script setup>
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import StatusTag from '@/components/common/StatusTag.vue'
import { settlementApi } from '@/api/settlement'
import { RECONCILE_STATUS, SETTLE_STATUS } from '@/constants/dict'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  settlementId: { type: [Number, String], default: null },
})
const emit = defineEmits(['update:modelValue', 'changed'])

const loading = ref(false)
const detail = ref(emptyDetail())

const matchedCount = computed(() => detail.value.items.filter((item) => item.reconcile === 'matched').length)
const differentCount = computed(() => detail.value.items.filter((item) => item.reconcile === 'different').length)

function emptyDetail() {
  return { items: [] }
}

async function load() {
  if (!props.settlementId) return
  loading.value = true
  try {
    detail.value = await settlementApi.detail(props.settlementId)
  } catch (error) {
    detail.value = emptyDetail()
  } finally {
    loading.value = false
  }
}

async function removeItem(row) {
  try {
    await ElMessageBox.confirm(`确认从结算单移除维修记录 ${row.repair_no} ?`, '移除明细', { type: 'warning' })
  } catch (error) {
    return
  }
  detail.value = await settlementApi.removeItem(props.settlementId, row.id)
  ElMessage.success('明细已移除')
  emit('changed')
}

async function confirmSettlement() {
  if (differentCount.value > 0) {
    try {
      await ElMessageBox.confirm(
        `存在 ${differentCount.value} 笔金额与维修费不一致, 确认仍要结算吗?`,
        '对账提示',
        { type: 'warning', confirmButtonText: '仍然确认' },
      )
    } catch (error) {
      return
    }
  } else {
    try {
      await ElMessageBox.confirm('确认后结算单将冻结, 确定继续?', '确认结算单', { type: 'warning' })
    } catch (error) {
      return
    }
  }
  detail.value = await settlementApi.confirm(props.settlementId)
  ElMessage.success('结算单已确认')
  emit('changed')
}
</script>

<style scoped>
.amount-strong {
  font-weight: 600;
  color: #f56c6c;
}

.reconcile-summary {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 16px 0 10px;
  flex-wrap: wrap;
  font-size: 13px;
}

.diff-bad {
  color: #f56c6c;
  font-weight: 600;
}

.diff-ok {
  color: #67c23a;
}

.footer-actions {
  margin-top: 18px;
  display: flex;
  align-items: center;
  gap: 12px;
}
</style>
