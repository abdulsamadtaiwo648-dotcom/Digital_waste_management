export type User = { id: number; full_name: string; email: string; phone: string; role: string }
export type Complaint = {
  reference: string; category: string; description: string; location: string; status: string; created_at: string
}
export type ComplaintUpdate = { status: string; note: string; created_at: string }
export type StaffRecord = { id: number; full_name: string; email: string; phone: string; role: string; created_at: string; active: boolean }
export type ContainerRecord = { id: string; label: string; service_area: string; usable_depth_cm: number; latest_fill_percent: number | null; latest_level: string | null; last_reading_at: string | null }
export type DeviceRecord = { id: string; container_id: string; container_label: string; enabled: boolean; simulated: boolean; latest_fill_percent: number | null; latest_level: string | null; last_reading_at: string | null }
export type PaymentRecord = { reference: string; service_code: string; amount_kobo: number; status: string; mode: string; created_at: string; receipt_reference?: string | null; location_id?: number | null; location_label?: string }
export type ResidentLocation = { id: number; area_id: number | null; label: string; address_line1: string; address_line2: string; locality: string; city: string; state: string; postal_code: string; latitude?: number; longitude?: number }
export type ServiceArea = { id: number; locality: string; city: string; state: string; active: boolean }

const API = window.location.pathname.endsWith('/admin.html')
  ? (import.meta.env.VITE_ADMIN_API_URL || 'http://localhost:8082/api/v1')
  : window.location.pathname.endsWith('/management.html')
    ? (import.meta.env.VITE_MANAGEMENT_API_URL || 'http://localhost:8081/api/v1')
    : (import.meta.env.VITE_RESIDENT_API_URL || import.meta.env.VITE_API_URL || 'http://localhost:8080/api/v1')

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`${API}${path}`, {
    ...init,
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...init.headers },
  })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({}))
    throw new Error(payload.message || `Request failed (${response.status})`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const api = {
  me: () => request<{ user: User }>('/auth/me'),
  updateProfile: (data: { full_name: string; phone: string }) => request<{ user: User }>('/auth/me', { method: 'PATCH', body: JSON.stringify(data) }),
  locations: () => request<{ locations: ResidentLocation[] }>('/locations'),
  serviceAreas: () => request<{ service_areas: ServiceArea[] }>('/service-areas'),
  createLocation: (data: { area_id: number; label: string; address_line1: string; address_line2: string; postal_code: string }) => request<{ location: ResidentLocation }>('/locations', { method: 'POST', body: JSON.stringify(data) }),
  updateLocation: (id: number, data: { area_id: number; label: string; address_line1: string; address_line2: string; postal_code: string }) => request<{ location: ResidentLocation }>(`/locations/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  deleteLocation: (id: number) => request<void>(`/locations/${id}`, { method: 'DELETE' }),
  login: (email: string, password: string) => request<{ user: User }>('/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  staffLogin: (email: string, password: string) => request<{ user: User }>('/auth/staff/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  staffMe: () => request<{ user: User }>('/auth/staff/me'),
  staffLogout: () => request<void>('/auth/staff/logout', { method: 'POST' }),
  forgotPassword: (email: string) => request<{ message: string; development_token?: string }>('/auth/forgot-password', { method: 'POST', body: JSON.stringify({ email }) }),
  resetPassword: (token: string, password: string) => request<{ message: string }>('/auth/reset-password', { method: 'POST', body: JSON.stringify({ token, password }) }),
  register: (data: { full_name: string; email: string; phone: string; password: string; terms_accepted: boolean }) => request<{ user: User }>('/auth/register', { method: 'POST', body: JSON.stringify(data) }),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  complaints: () => request<{ complaints: Complaint[] }>('/complaints'),
  complaint: (reference: string) => request<{ complaint: Complaint; history: ComplaintUpdate[] }>(`/complaints/${encodeURIComponent(reference)}`),
  createComplaint: (data: { category: string; description: string; location_id: number }) => request<{ complaint: Complaint }>('/complaints', { method: 'POST', body: JSON.stringify(data) }),
  paymentOptions: (locationId = '') => request<{ enabled: boolean; simulated: boolean; fee_amount_kobo: number; area_name?: string; message: string }>(`/payments/options${locationId ? `?location_id=${encodeURIComponent(locationId)}` : ''}`),
  payments: () => request<{ payments: PaymentRecord[] }>('/payments'),
  createPayment: (idempotencyKey: string, location_id: number) => request<{ payment: PaymentRecord; simulated: boolean }>('/payments', { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ service_code: 'waste_fee', location_id }) }),
  simulatePayment: (reference: string) => request<{ reference: string; status: string; simulated: boolean; receipt_reference: string }>(`/payments/${encodeURIComponent(reference)}/simulate`, { method: 'POST' }),
  receipt: (reference: string) => request<{ receipt: { reference: string; payment_reference: string; service_code: string; status: string; created_at: string; amount_kobo: number; simulated: boolean } }>(`/receipts/${encodeURIComponent(reference)}`),
  operationsComplaints: (status = '') => request<{ items: { complaint: Complaint; citizen: { full_name: string; email: string; phone: string } }[] }>(`/operations/complaints${status ? `?status=${encodeURIComponent(status)}` : ''}`),
  operationsPayments: () => request<{ items: { reference: string; amount_kobo: number; status: string; mode: string; created_at: string; resident: { full_name: string; email: string; phone: string }; location: { label: string; address: string } }[] }>('/operations/payments'),
  updateComplaint: (reference: string, data: { status: string; public_message: string; internal_note: string }) => request<{ reference: string; status: string }>(`/operations/complaints/${encodeURIComponent(reference)}`, { method: 'PATCH', body: JSON.stringify(data) }),
  containers: () => request<{ containers: ContainerRecord[] }>('/operations/containers'),
  createContainer: (data: { label: string; service_area: string; usable_depth_cm: number; minimum_distance_cm: number; installation_offset_cm: number }) => request<{ id: string }>('/operations/containers', { method: 'POST', body: JSON.stringify(data) }),
  collections: () => request<{ collections: { id: number; container_id: string; container_label: string; assigned_to: number; assigned_name: string; scheduled_at: string; status: string; completion_note: string }[] }>('/operations/collections'),
  inspections: () => request<{ inspections: { id: number; container_id: string; container_label: string; inspector: string; condition: string; notes: string; inspected_at: string }[] }>('/operations/inspections'),
  operationsStaff: () => request<{ staff: { id: number; full_name: string }[] }>('/operations/staff'),
  createCollection: (data: { container_id: string; assigned_to: number; scheduled_at: string }) => request<{ id: number }>('/operations/collections', { method: 'POST', body: JSON.stringify(data) }),
  updateCollection: (id: number, data: { status: string; completion_note: string }) => request<{ id: number; status: string }>(`/operations/collections/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  analytics: () => request<{ registered_containers: number; offline_devices: number; active_complaints: number; overdue_collections: number; completed_collections_30d: number; average_resolution_hours_30d: number | null; weekly_trends: { week: string; complaints_opened: number; complaints_resolved: number; collections_completed: number }[] }>('/operations/analytics'),
  notifications: () => request<{ notifications: { id: number; title: string; message: string; resource_type: string; resource_id: string; created_at: string }[] }>('/operations/notifications'),
  announcements: (staff = false) => request<{ announcements: { id: number; title: string; body: string; status: string; published_at: string | null; created_at: string; author: string }[] }>(staff ? '/operations/announcements' : '/announcements'),
  createAnnouncement: (data: { title: string; body: string; status: string }) => request<{ id: number; status: string }>('/operations/announcements', { method: 'POST', body: JSON.stringify(data) }),
  staff: () => request<{ staff: StaffRecord[] }>('/admin/users'),
  createStaff: (data: { full_name: string; email: string; phone: string; password: string; role: string }) => request<{ id: number; role: string }>('/admin/users', { method: 'POST', body: JSON.stringify(data) }),
  updateStaff: (id: number, data: { role: string; active: boolean }) => request<{ id: number; role: string; active: boolean }>(`/admin/users/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  devices: () => request<{ devices: DeviceRecord[] }>('/admin/devices'),
  adminContainers: () => request<{ containers: ContainerRecord[] }>('/admin/containers'),
  createDevice: (data: { id?: string; container_id: string; simulated: boolean }) => request<{ id: string; token: string; warning: string; simulated: boolean }>('/admin/devices', { method: 'POST', body: JSON.stringify(data) }),
  createInspection: (data: { container_id: string; condition: string; notes: string }) => request<{ id: number }>('/operations/inspections', { method: 'POST', body: JSON.stringify(data) }),
  updateDevice: (id: string, data: { enabled?: boolean; rotate_token?: boolean }) => request<{ id: string; enabled?: boolean; token?: string; warning?: string }>(`/admin/devices/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(data) }),
  auditLogs: () => request<{ audit_logs: { id: number; actor: string; action: string; resource_type: string; resource_id: string; details: unknown; created_at: string }[] }>('/admin/audit-logs'),
  settings: () => request<{ settings: Record<string, number> }>('/admin/settings'),
  updateSettings: (data: Record<string, number>) => request<{ updated: boolean }>('/admin/settings', { method: 'PATCH', body: JSON.stringify(data) }),
  areaFees: () => request<{ area_fees: { id: number; area_id: number | null; area_name: string; locality: string | null; city: string | null; state: string | null; resolved: boolean; amount_kobo: number; updated_at: string }[] }>('/admin/area-fees'),
  serviceAreasAdmin: () => request<{ service_areas: ServiceArea[] }>('/admin/service-areas'),
  createServiceArea: (data: { locality: string; city: string; state: string }) => request<{ service_area: ServiceArea }>('/admin/service-areas', { method: 'POST', body: JSON.stringify(data) }),
  updateServiceArea: (id: number, active: boolean) => request<{ service_area: ServiceArea }>(`/admin/service-areas/${id}`, { method: 'PATCH', body: JSON.stringify({ active }) }),
  saveAreaFee: (area_id: number, amount_kobo: number, legacy_fee_id?: number) => request<{ id: number }>('/admin/area-fees', { method: 'PUT', body: JSON.stringify({ area_id, amount_kobo, legacy_fee_id }) }),
}
