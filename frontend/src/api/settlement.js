import request from './request'

// 费用结算接口。
export const settlementApi = {
  list: (params) => request.get('/settlements', { params }),
  candidates: (params) => request.get('/settlements/repair-candidates', { params }),
  detail: (id) => request.get(`/settlements/${id}`),
  create: (data) => request.post('/settlements', data),
  update: (id, data) => request.put(`/settlements/${id}`, data),
  remove: (id) => request.delete(`/settlements/${id}`),
  confirm: (id, data) => request.post(`/settlements/${id}/confirm`, data || {}),
  addItem: (id, data) => request.post(`/settlements/${id}/items`, data),
  removeItem: (id, itemId) => request.delete(`/settlements/${id}/items/${itemId}`),
}
