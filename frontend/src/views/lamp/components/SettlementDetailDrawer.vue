<template>
  <el-drawer
    :model-value="modelValue"
    title="结算单详情"
    size="640px"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div v-loading="loading">
      <template v-if="detail">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="结算单号">{{ detail.settle_no }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <StatusTag :dict="SETTLE_STATUS" :value="detail.status" />
          </el-descriptions-item>
          <el-descriptions-item label="标题" :span="2">{{ detail.title }}</el-descriptions-item>
          <el-descriptions-item label="维修班组">{{ detail.repair_team || '-' }}</el-descriptions-item>
          <el-descriptions-item label="确认时间">{{ formatDateTime(detail.confirmed_at) }}</el-descriptions-item>
          <el-descriptions-item label="结算总额">
            <span class="total-amount">{{ formatMoney(detail.total_amount) }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="明细条数">{{ detail.items?.length || 0 }} 条</el-descriptions-item>
        </el-descriptions>

        <div class="drawer-section-title">费用明细(逐条对账)</div>
        <el-table :data="detail.items" size="small" stripe>
          <el-table-column prop="repair_no" label="维修单号" width="130" />
          <el-table-column prop="lamp_code" label="路灯" width="100" />
          <el-table-column label="结算金额" width="110">
            <template #default="{ row }">{{ formatMoney(row.amount) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="80" v-if="detail.status === 'draft'">
            <template #default="{ row }">
              <el-button link type="danger" @click="removeItem(row)">移除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="drawer-tip text-muted">
          结算单确认后金额锁定; 履历中每条维修费用将与本单明细逐条核对, 金额一致显示"已对平", 否则显示"金额不符"。
        </div>

        <div class="drawer-footer">
          <el-button
            v-if="detail.status === 'draft'"
            type="success"
            :loading="confirming"
            @click="handleConfirm"
          >确认结算单</el-button>
          <el-button @click="$emit('update:modelValue', false)">关闭</el-button>
        </div>
      </template>
    </div>
  </el-drawer>
</template>

<script setup>
import { ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import StatusTag from '@/components/common/StatusTag.vue'
import { settlementApi } from '@/api/settlement'
import { SETTLE_STATUS } from '@/constants/dict'
import { formatDateTime, formatMoney } from '@/utils/format'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  settlementId: { type: Number, default: null },
})
const emit = defineEmits(['update:modelValue', 'changed'])

const loading = ref(false)
const confirming = ref(false)
const detail = ref(null)

watch(
  () => [props.modelValue, props.settlementId],
  ([visible]) => {
    if (visible && props.settlementId) load()
  },
)

async function load() {
  loading.value = true
  try {
    detail.value = await settlementApi.detail(props.settlementId)
  } catch (error) {
    detail.value = null
  } finally {
    loading.value = false
  }
}

async function removeItem(item) {
  try {
    await ElMessageBox.confirm(`确认从结算单移除明细 ${item.repair_no} ?`, '移除确认', {
      type: 'warning',
      confirmButtonText: '移除',
      cancelButtonText: '取消',
    })
  } catch (error) {
    return
  }
  try {
    await settlementApi.removeItem(props.settlementId, item.id)
    ElMessage.success('明细已移除')
    await load()
    emit('changed')
  } catch (error) {
    // 错误提示由请求拦截器统一处理
  }
}

async function handleConfirm() {
  try {
    await ElMessageBox.confirm('确认后结算金额将锁定, 不能再增删明细, 确认继续?', '确认结算单', {
      type: 'warning',
      confirmButtonText: '确认',
      cancelButtonText: '取消',
    })
  } catch (error) {
    return
  }
  confirming.value = true
  try {
    await settlementApi.confirm(props.settlementId)
    ElMessage.success('结算单已确认')
    await load()
    emit('changed')
  } catch (error) {
    // 错误提示由请求拦截器统一处理
  } finally {
    confirming.value = false
  }
}
</script>

<style scoped>
.total-amount {
  font-weight: 600;
  color: var(--el-color-danger);
}

.drawer-section-title {
  margin: 16px 0 8px;
  font-weight: 600;
}

.drawer-tip {
  margin-top: 10px;
  font-size: 12px;
  line-height: 1.6;
}

.drawer-footer {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
