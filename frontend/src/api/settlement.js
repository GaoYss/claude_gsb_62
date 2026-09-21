import request from './request'

// 结算单接口。
export const settlementApi = {
  list: (params) => request.get('/settlements', { params }),
  detail: (id) => request.get(`/settlements/${id}`),
  create: (data) => request.post('/settlements', data),
  remove: (id) => request.delete(`/settlements/${id}`),
  addItem: (id, data) => request.post(`/settlements/${id}/items`, data),
  removeItem: (id, itemId) => request.delete(`/settlements/${id}/items/${itemId}`),
  confirm: (id) => request.post(`/settlements/${id}/confirm`),
}
