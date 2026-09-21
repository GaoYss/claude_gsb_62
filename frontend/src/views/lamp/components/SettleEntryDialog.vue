<template>
  <el-dialog
    :model-value="modelValue"
    title="登记结算"
    width="460px"
    @update:model-value="$emit('update:modelValue', $event)"
    @closed="handleClosed"
  >
    <el-form v-if="repair" label-width="92px">
      <el-form-item label="维修单号">
        <span>{{ repair.repair_no }}</span>
      </el-form-item>
      <el-form-item label="故障单号">
        <span>{{ repair.fault_no }}</span>
      </el-form-item>
      <el-form-item label="维修费用">
        <span class="cost-text">{{ formatMoney(repair.cost) }}</span>
        <span class="text-muted cost-hint">结算金额默认取维修费用</span>
      </el-form-item>
      <el-form-item label="结算方式">
        <el-radio-group v-model="form.mode">
          <el-radio value="existing">归入已有草稿单</el-radio>
          <el-radio value="new">新建结算单</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="form.mode === 'existing'" label="选择结算单">
        <el-select v-model="form.settlement_id" placeholder="选择草稿结算单" style="width: 100%" @visible-change="loadDrafts">
          <el-option
            v-for="item in drafts"
            :key="item.id"
            :label="`${item.settle_no} ${item.title}`"
            :value="item.id"
          />
        </el-select>
        <div v-if="drafts.length === 0" class="text-muted no-draft">暂无草稿结算单, 可直接新建</div>
      </el-form-item>
      <template v-if="form.mode === 'new'">
        <el-form-item label="结算单标题" required>
          <el-input v-model="form.title" maxlength="128" placeholder="例如: 9 月维修费用结算单" />
        </el-form-item>
        <el-form-item label="维修班组">
          <el-input v-model="form.repair_team" maxlength="64" />
        </el-form-item>
      </template>
      <el-form-item label="结算金额" required>
        <el-input-number v-model="form.amount" :min="0" :precision="2" :step="10" controls-position="right" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="$emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="handleSubmit">确定</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { settlementApi } from '@/api/settlement'
import { formatMoney } from '@/utils/format'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  repair: { type: Object, default: null },
})
const emit = defineEmits(['update:modelValue', 'saved'])

const submitting = ref(false)
const drafts = ref([])
const form = reactive({
  mode: 'existing',
  settlement_id: null,
  title: '',
  repair_team: '',
  amount: 0,
})

watch(
  () => props.repair,
  (repair) => {
    if (!repair) return
    form.mode = 'existing'
    form.settlement_id = null
    form.title = ''
    form.repair_team = repair.repair_team || ''
    form.amount = Number((repair.cost ?? 0).toFixed(2))
  },
)

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) loadDrafts()
  },
)

async function loadDrafts() {
  if (!props.modelValue) return
  try {
    const data = await settlementApi.list({ status: 'draft', page: 1, page_size: 50 })
    drafts.value = data.items || []
    if (drafts.value.length === 0) {
      form.mode = 'new'
    }
  } catch (error) {
    drafts.value = []
  }
}

async function handleSubmit() {
  if (form.mode === 'existing' && !form.settlement_id) {
    ElMessage.warning('请选择一张草稿结算单')
    return
  }
  if (form.mode === 'new' && !form.title.trim()) {
    ElMessage.warning('请填写结算单标题')
    return
  }

  submitting.value = true
  try {
    let settlementId = form.settlement_id
    if (form.mode === 'new') {
      const created = await settlementApi.create({
        title: form.title.trim(),
        repair_team: form.repair_team.trim(),
      })
      settlementId = created.id
    }
    await settlementApi.addItem(settlementId, {
      repair_id: props.repair.repair_id,
      amount: form.amount,
    })
    ElMessage.success('已加入结算单, 可在结算单确认后完成对账')
    emit('update:modelValue', false)
    emit('saved')
  } catch (error) {
    // 错误提示由请求拦截器统一处理
  } finally {
    submitting.value = false
  }
}

function handleClosed() {
  form.settlement_id = null
  form.title = ''
}
</script>

<style scoped>
.cost-text {
  font-weight: 600;
}

.cost-hint {
  margin-left: 8px;
  font-size: 12px;
}

.no-draft {
  font-size: 12px;
  margin-top: 4px;
}
</style>
