import request from './request'

// 维修状态查询接口。
export const statusApi = {
  overview: () => request.get('/status/overview'),
  lamps: (params) => request.get('/status/lamps', { params }),
  track: (params) => request.get('/status/track', { params }),
  // 单盏路灯的完整维修履历(分页 + 故障类型/时间段过滤 + 累计与对账统计)。
  lampHistory: (lampId, params) => request.get(`/status/lamps/${lampId}/history`, { params }),
}
