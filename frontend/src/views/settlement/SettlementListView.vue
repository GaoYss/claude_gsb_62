<template>
  <div class="page">
    <PageHeader title="费用结算" description="按维修记录逐条登记结算金额, 履历中的维修费用可与结算单逐条对账">
      <el-button :icon="Refresh" @click="load">刷新</el-button>
      <el-button type="primary" :icon="Plus" @click="openCreate">新建结算单</el-button>
    </PageHeader>

    <el-card shadow="never">
      <div class="filter-bar">
        <el-input v-model="query.keyword" placeholder="结算单号 / 标题 / 经办人" clearable @keyup.enter="search" />
        <el-select v-model="query.status" placeholder="状态" clearable style="width: 140px">
          <el-option v-for="(item, key) in SETTLE_STATUS" :key="key" :label="item.label" :value="key" />
        </el-select>
        <el-button type="primary" :icon="Search" @click="search">查询</el-button>
        <el-button :icon="RefreshLeft" @click="reset">重置</el-button>
      </div>
    </el-card>

    <el-card shadow="never">
      <el-table v-loading="loading" :data="rows" stripe>
        <el-table-column prop="settle_no" label="结算单号" width="160" fixed="left" />
        <el-table-column prop="title" label="标题" min-width="200" show-overflow-tooltip />
        <el-table-column label="结算周期" width="200">
          <template #default="{ row }">
            {{ formatDate(row.period_start) }} 至 {{ formatDate(row.period_end) }}
          </template>
        </el-table-column>
        <el-table-column prop="item_count" label="明细笔数" width="90" align="right" />
        <el-table-column label="金额合计" width="130" align="right">
          <template #default="{ row }">{{ formatMoney(row.total_amount) }}</template>
        </el-table-column>
        <el-table-column prop="operator" label="经办人" width="100" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }"><StatusTag :dict="SETTLE_STATUS" :value="row.status" /></template>
        </el-table-column>
        <el-table-column label="确认时间" width="150">
          <template #default="{ row }">{{ formatDateTime(row.confirmed_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row)">详情/对账</el-button>
            <el-button v-if="row.status === 'draft'" link type="danger" @click="handleDelete(row)">删除</el-button>
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

    <SettlementFormDialog v-model="createVisible" @saved="handleSaved" />
    <SettlementDetailDrawer v-model="detailVisible" :settlement-id="detailId" @changed="load" />
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, RefreshLeft, Search } from '@element-plus/icons-vue'
import PageHeader from '@/components/common/PageHeader.vue'
import StatusTag from '@/components/common/StatusTag.vue'
import DataPagination from '@/components/common/DataPagination.vue'
import SettlementFormDialog from './components/SettlementFormDialog.vue'
import SettlementDetailDrawer from './components/SettlementDetailDrawer.vue'
import { settlementApi } from '@/api/settlement'
import { SETTLE_STATUS } from '@/constants/dict'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'
import { useListPage } from '@/composables/useListPage'

const { loading, rows, total, query, load, search, reset, changePage, changePageSize } = useListPage(
  settlementApi.list,
  { keyword: '', status: '' },
)

const createVisible = ref(false)
const detailVisible = ref(false)
const detailId = ref(null)

function openCreate() {
  createVisible.value = true
}

function openDetail(row) {
  detailId.value = row.id
  detailVisible.value = true
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(`确认删除草稿结算单 ${row.settle_no} ?`, '删除确认', { type: 'warning' })
  } catch (error) {
    return
  }
  await settlementApi.remove(row.id)
  ElMessage.success('结算单已删除')
  load()
}

function handleSaved() {
  load()
}
</script>
