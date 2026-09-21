<template>
  <el-dialog
    :model-value="modelValue"
    title="新建结算单"
    width="960px"
    @update:model-value="$emit('update:modelValue', $event)"
    @open="syncForm"
  >
    <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
      <el-row :gutter="16">
        <el-col :span="12">
          <el-form-item label="标题" prop="title">
            <el-input v-model="form.title" placeholder="例如: 2026年6月维修费用结算单" />
          </el-form-item>
        </el-col>
        <el-col :span="6">
          <el-form-item label="经办人">
            <el-input v-model="form.operator" />
          </el-form-item>
        </el-col>
        <el-col :span="6">
          <el-form-item label="结算周期">
            <el-date-picker
              v-model="period"
              type="daterange"
              value-format="YYYY-MM-DD"
              range-separator="至"
              start-placeholder="开始"
              end-placeholder="结束"
              style="width: 100%"
            />
          </el-form-item>
        </el-col>
      </el-row>

      <div class="candidate-header">
        <el-input
          v-model="keyword"
          placeholder="维修单号 / 故障单号 / 路灯编号 / 维修人员"
          clearable
          style="width: 320px"
          @keyup.enter="searchCandidates"
        />
        <el-date-picker
          v-model="finishedRange"
          type="daterange"
          value-format="YYYY-MM-DD"
          range-separator="至"
          start-placeholder="完工起"
          end-placeholder="完工止"
          style="width: 260px"
        />
        <el-button type="primary" :icon="Search" @click="searchCandidates">查询</el-button>
        <span class="text-muted candidate-hint">仅列出已完工且未结算的维修记录, 勾选后可调整结算金额</span>
      </div>

      <el-table
        ref="tableRef"
        v-loading="candidateLoading"
        :data="candidates"
        height="300"
        size="small"
        border
        row-key="repair_id"
        @selection-change="handleSelectionChange"
      >
        <el-table-column type="selection" width="42" reserve-selection />
        <el-table-column prop="repair_no" label="维修单号" width="140" />
        <el-table-column prop="lamp_code" label="路灯编号" width="100" />
        <el-table-column prop="fault_type" label="故障类型" width="90" />
        <el-table-column prop="repair_team" label="班组" width="110" />
        <el-table-column prop="repairman" label="人员" width="80" />
        <el-table-column label="完工时间" width="140">
          <template #default="{ row }">{{ formatDateTime(row.finished_at) }}</template>
        </el-table-column>
        <el-table-column prop="materials" label="灯具/耗材" min-width="130" show-overflow-tooltip />
        <el-table-column label="维修费" width="90" align="right">
          <template #default="{ row }">{{ formatMoney(row.cost) }}</template>
        </el-table-column>
      </el-table>
      <DataPagination
        :page="candidatePage.page"
        :page-size="candidatePage.page_size"
        :total="candidateTotal"
        @page-change="changeCandidatePage"
        @size-change="changeCandidateSize"
      />

      <div class="picked-title">已选明细 ({{ picked.length }} 笔, 合计 {{ formatMoney(pickedTotal) }})</div>
      <el-table :data="picked" height="200" size="small" border>
        <el-table-column prop="repair_no" label="维修单号" width="150" />
        <el-table-column prop="lamp_code" label="路灯" width="100" />
        <el-table-column prop="materials" label="灯具/耗材" min-width="140" show-overflow-tooltip />
        <el-table-column label="维修费" width="110" align="right">
          <template #default="{ row }">{{ formatMoney(row.cost) }}</template>
        </el-table-column>
        <el-table-column label="结算金额" width="150">
          <template #default="{ row }">
            <el-input-number
              v-model="amountMap[row.repair_id]"
              :min="0"
              :precision="2"
              :step="10"
              size="small"
              style="width: 130px"
            />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="70">
          <template #default="{ row }">
            <el-button link type="danger" @click="removePicked(row)">移除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-form>

    <template #footer>
      <el-button @click="$emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="handleSubmit">保存草稿</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, nextTick, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import DataPagination from '@/components/common/DataPagination.vue'
import { settlementApi } from '@/api/settlement'
import { formatDateTime, formatMoney } from '@/utils/format'

defineProps({
  modelValue: { type: Boolean, default: false },
})
const emit = defineEmits(['update:modelValue', 'saved'])

const formRef = ref(null)
const tableRef = ref(null)
const submitting = ref(false)

const form = reactive({ title: '', operator: '', remark: '' })
const period = ref(null)
const keyword = ref('')
const finishedRange = ref(null)

const candidateLoading = ref(false)
const candidates = ref([])
const candidateTotal = ref(0)
const candidatePage = reactive({ page: 1, page_size: 10 })
const picked = ref([])
const amountMap = reactive({})

const rules = {
  title: [{ required: true, message: '请填写结算单标题', trigger: 'blur' }],
}

const pickedTotal = computed(() =>
  picked.value.reduce((sum, row) => sum + Number(amountMap[row.repair_id] ?? row.cost ?? 0), 0),
)

function syncForm() {
  form.title = ''
  form.operator = ''
  form.remark = ''
  period.value = null
  keyword.value = ''
  finishedRange.value = null
  picked.value = []
  Object.keys(amountMap).forEach((key) => delete amountMap[key])
  candidatePage.page = 1
  candidatePage.page_size = 10
  nextTick(() => tableRef.value?.clearSelection?.())
  loadCandidates()
}

async function loadCandidates() {
  candidateLoading.value = true
  try {
    const params = {
      ...candidatePage,
      keyword: keyword.value,
      start_date: finishedRange.value?.[0],
      end_date: finishedRange.value?.[1],
    }
    const data = await settlementApi.candidates(params)
    candidates.value = data?.items ?? []
    candidateTotal.value = data?.total ?? 0
    // reserve-selection 跨页保留, 数据刷新后需重新勾选当前页已选项。
    await nextTick()
    candidates.value.forEach((row) => {
      if (picked.value.some((item) => item.repair_id === row.repair_id)) {
        tableRef.value?.toggleRowSelection(row, true)
      }
    })
  } catch (error) {
    candidates.value = []
  } finally {
    candidateLoading.value = false
  }
}

function searchCandidates() {
  candidatePage.page = 1
  loadCandidates()
}

function changeCandidatePage(page) {
  candidatePage.page = page
  loadCandidates()
}

function changeCandidateSize(size) {
  candidatePage.page = 1
  candidatePage.page_size = size
  loadCandidates()
}

function handleSelectionChange(selection) {
  const existing = picked.value.filter((row) => !candidates.value.some((c) => c.repair_id === row.repair_id))
  const merged = [...existing, ...selection]
  const unique = new Map()
  merged.forEach((row) => {
    unique.set(row.repair_id, row)
    if (amountMap[row.repair_id] === undefined) {
      amountMap[row.repair_id] = Number(row.cost ?? 0)
    }
  })
  picked.value = Array.from(unique.values())
}

function removePicked(row) {
  tableRef.value?.toggleRowSelection(row, false)
  picked.value = picked.value.filter((item) => item.repair_id !== row.repair_id)
  delete amountMap[row.repair_id]
}

async function handleSubmit() {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  if (picked.value.length === 0) {
    ElMessage.warning('请至少勾选一条维修记录作为结算明细')
    return
  }
  submitting.value = true
  try {
    await settlementApi.create({
      title: form.title,
      operator: form.operator,
      remark: form.remark,
      period_start: period.value?.[0] || '',
      period_end: period.value?.[1] || '',
      items: picked.value.map((row) => ({
        repair_id: row.repair_id,
        amount: Number(amountMap[row.repair_id] ?? row.cost ?? 0),
      })),
    })
    ElMessage.success('结算单草稿已创建')
    emit('update:modelValue', false)
    emit('saved')
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.candidate-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 6px 0 10px;
  flex-wrap: wrap;
}

.candidate-hint {
  font-size: 12px;
}

.picked-title {
  font-weight: 600;
  margin: 14px 0 8px;
}
</style>
