export type User = { id: number; full_name: string; email: string; phone: string; role: string }
export type Complaint = {
  reference: string; category: string; description: string; location: string; status: string; created_at: string
}
export type ComplaintUpdate = { status: string; note: string; created_at: string }
export type StaffRecord = { id: number; full_name: string; email: string; phone: string; role: string; created_at: string; active: boolean }
export type ContainerRecord = { id: string; label: string; service_area: string; usable_depth_cm: number; active_sensor_count: number; latest_fill_percent: number | null; latest_level: string | null; last_reading_at: string | null }
export type DeviceRecord = { id: string; container_id: string; container_label: string; container_depth_cm: number; minimum_distance_cm: number; installation_offset_cm: number; enabled: boolean; simulated: boolean; latest_fill_percent: number | null; latest_level: string | null; last_reading_at: string | null }
export type ServiceFeeBreakdown = { area_flat_fee_kobo: number; base_bin_fee_kobo?: number; bin_size_fee_kobo: number; delivered_bins: { bin_id: number; size_litres: number; amount_kobo: number }[] }
export type SensorReading = { id: number; device_id: string; measured_at: string; received_at: string; distance_cm: number; fill_percent: number; level: string; simulated: boolean }
export type PaymentRecord = { reference: string; service_code: string; amount_kobo: number; status: string; mode: string; created_at: string; receipt_reference?: string | null; location_id?: number | null; location_label?: string }
export type MonthlyServiceInvoice = { reference: string; location_id: number; billing_month: string; due_date: string; amount_kobo: number; status: 'due' | 'overdue' | 'paid' | 'void'; created_at: string }
export type ResidentLocation = { id: number; area_id: number | null; label: string; address_line1: string; address_line2: string; locality: string; city: string; state: string; postal_code: string; latitude?: number; longitude?: number; property_registered?: boolean; property_relationship?: string }
export type ServiceArea = { id: number; locality: string; city: string; state: string; active: boolean }
export type PropertyUnit = { id: number; code: string; unit_type: string; floor_label: string; service_scope: string }
export type BinEvacuationEvent = { id: number; evacuated_at: string; staff_name: string; note?: string }
export type PropertyBin = { id: number; tracking_id: string; size_litres: number; status: string; unit_code?: string; registered_at: string; delivered_at?: string; delivered_by?: string; delivery_note?: string; assigned_to_you?: boolean; assigned_resident?: string; last_evacuated_at?: string; evacuated_by?: string }
export type RegisteredProperty = { id: number; location_id: number; address: string; area: string; property_type: string; structure_type: string; floors: number; registration_scope: string; registrant_role: string; status: string; review_status: string; review_reason?: string; units: PropertyUnit[]; bins: PropertyBin[] }
export type PropertyRegistration = { id: number; status: string; review_status: string; review_reason?: string; property_type: string; structure_type: string; registration_scope: string; registrant_role: string; address: string; resident: string; phone: string; units: { id: number; code: string; service_scope: string }[]; bins: PropertyBin[] }
export type PropertyApplicationSummary = { id: number; review_status: string; property_type: string; structure_type: string; registration_scope: string; resident: string; area: string; created_at: string; unit_count: number; bin_count: number }
export type PropertyUnitRequest = { id: number; property_id: number; code: string; unit_type: string; floor_label: string; service_scope: string; relationship: string; resident: string; phone: string; address: string }

const pathRole = window.location.pathname.endsWith('/admin.html')
  ? 'admin'
  : window.location.pathname.endsWith('/management.html')
    ? 'management'
    : undefined
const appRole = pathRole || import.meta.env.VITE_APP_ROLE || 'resident'
const configuredAPI = appRole === 'admin'
  ? import.meta.env.VITE_ADMIN_API_URL
  : appRole === 'management'
    ? import.meta.env.VITE_MANAGEMENT_API_URL
    : import.meta.env.VITE_RESIDENT_API_URL || import.meta.env.VITE_API_URL
const localAPI = appRole === 'admin'
  ? 'http://localhost:8082/api/v1'
  : appRole === 'management'
    ? 'http://localhost:8081/api/v1'
    : 'http://localhost:8080/api/v1'

if (appRole !== 'resident' && appRole !== 'management' && appRole !== 'admin') {
  throw new Error(`VITE_APP_ROLE must be resident, management, or admin (got "${appRole}")`)
}
const configuredRoot = configuredAPI?.replace(/\/+$/, '')
const renderResidentProxy = appRole === 'resident' && window.location.hostname.endsWith('.onrender.com')
const API = renderResidentProxy
  ? '/api/v1'
  : configuredRoot
    ? configuredRoot.endsWith('/api/v1') ? configuredRoot : `${configuredRoot}/api/v1`
    : import.meta.env.DEV
      ? localAPI
      : ''

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!API) throw new Error(`The ${appRole} API URL is not configured for this deployment`)
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
  createLocation: (data: { area_id: number; label: string; address_line1: string; address_line2: string; postal_code: string; latitude?: number; longitude?: number }) => request<{ location: ResidentLocation }>('/locations', { method: 'POST', body: JSON.stringify(data) }),
  updateLocation: (id: number, data: { area_id: number; label: string; address_line1: string; address_line2: string; postal_code: string; latitude?: number; longitude?: number }) => request<{ location: ResidentLocation }>(`/locations/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  deleteLocation: (id: number) => request<void>(`/locations/${id}`, { method: 'DELETE' }),
  properties: () => request<{ properties: RegisteredProperty[] }>('/properties'),
  binEvacuationHistory: (id: number, beforeId?: number) => request<{ events: BinEvacuationEvent[]; has_more: boolean; next_before_id: number | null }>(`/bins/${id}/evacuations${beforeId ? `?before_id=${beforeId}` : ''}`),
  registerProperty: (data: { location_id: number; property_type: string; structure_type: string; floors: number; registration_scope: string; registrant_role: string; units: { code: string; unit_type: string; floor_label: string; service_scope: string }[] }) => request<{ id: number }>('/properties', { method: 'POST', body: JSON.stringify(data) }),
  requestPropertyUnit: (data: { location_id: number; code: string; unit_type: string; floor_label: string; service_scope: string; relationship: string }) => request<{ id: number }>('/properties/unit-requests', { method: 'POST', body: JSON.stringify(data) }),
  residentPropertyUnitRequests: () => request<{ requests: { id: number; code: string; floor_label: string; relationship: string; status: string; review_reason: string; created_at: string; reviewed_at?: string }[] }>('/properties/unit-requests'),
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
  paymentOptions: (locationId = '') => request<{ enabled: boolean; simulated: boolean; fee_amount_kobo: number; fee_breakdown?: ServiceFeeBreakdown; area_name?: string; message: string }>(`/payments/options${locationId ? `?location_id=${encodeURIComponent(locationId)}` : ''}`),
  payments: () => request<{ payments: PaymentRecord[] }>('/payments'),
  monthlyInvoices: (locationId = '') => request<{ invoices: MonthlyServiceInvoice[] }>(`/monthly-invoices${locationId ? `?location_id=${encodeURIComponent(locationId)}` : ''}`),
  createPayment: (idempotencyKey: string, location_id: number) => request<{ payment: PaymentRecord; simulated: boolean }>('/payments', { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ service_code: 'waste_fee', location_id }) }),
  simulatePayment: (reference: string) => request<{ reference: string; status: string; simulated: boolean; receipt_reference: string }>(`/payments/${encodeURIComponent(reference)}/simulate`, { method: 'POST' }),
  receipt: (reference: string) => request<{ receipt: { reference: string; payment_reference: string; service_code: string; status: string; created_at: string; amount_kobo: number; simulated: boolean } }>(`/receipts/${encodeURIComponent(reference)}`),
  operationsComplaints: (status = '') => request<{ items: { complaint: Complaint; citizen: { full_name: string; email: string; phone: string } }[] }>(`/operations/complaints${status ? `?status=${encodeURIComponent(status)}` : ''}`),
  operationsPayments: () => request<{ items: { reference: string; amount_kobo: number; status: string; mode: string; created_at: string; resident: { full_name: string; email: string; phone: string }; location: { label: string; address: string } }[] }>('/operations/payments'),
  updateComplaint: (reference: string, data: { status: string; public_message: string; internal_note: string }) => request<{ reference: string; status: string }>(`/operations/complaints/${encodeURIComponent(reference)}`, { method: 'PATCH', body: JSON.stringify(data) }),
  containers: () => request<{ containers: ContainerRecord[] }>('/operations/containers'),
  containerReadings: (id: string) => request<{ readings: SensorReading[] }>(`/operations/containers/${encodeURIComponent(id)}/readings`),
  operationsProperties: (page = 1, search = '', status = '') => { const params = new URLSearchParams({ page: String(page), page_size: '25' }); if (search) params.set('search', search); if (status) params.set('status', status); return request<{ properties: PropertyApplicationSummary[]; total: number; page: number; page_size: number }>(`/operations/properties?${params}`) },
  operationsPropertyDetails: (id: number) => request<{ property: PropertyRegistration & { email: string } }>(`/operations/properties/${id}`),
  reviewPropertyRegistration: (id: number, decision: 'approved' | 'rejected', reason: string) => request<{ status: string }>(`/operations/properties/${id}/review`, { method: 'PATCH', body: JSON.stringify({ decision, reason }) }),
  operationsPropertyUnitRequests: () => request<{ requests: PropertyUnitRequest[] }>('/operations/property-unit-requests'),
  reviewPropertyUnitRequest: (id: number, decision: 'approved' | 'rejected', reason: string) => request<{ status: string }>(`/operations/property-unit-requests/${id}`, { method: 'PATCH', body: JSON.stringify({ decision, reason }) }),
  operationsServiceAreas: () => request<{ service_areas: ServiceArea[] }>('/operations/service-areas'),
  assistedPropertyRegistration: (data: { resident_email: string; create_account: boolean; resident_name?: string; resident_phone?: string; password?: string; terms_accepted?: boolean; area_id: number; label: string; address_line1: string; address_line2: string; postal_code: string; property_type: string; structure_type: string; floors: number; registration_scope: string; registrant_role: string; units: { code: string; unit_type: string; floor_label: string; service_scope: string }[] }) => request<{ id: number; account_created: boolean }>('/operations/assisted-properties', { method: 'POST', body: JSON.stringify(data) }),
  createPropertyBin: (propertyId: number, data: { unit_id?: number | null; resident_email?: string; size_litres: number }) => request<{ id: number; tracking_id: string }>(`/operations/properties/${propertyId}/bins`, { method: 'POST', body: JSON.stringify(data) }),
  recordBinDelivery: (id: number, data: { outcome: 'delivered' | 'failed'; note: string }) => request<{ status: string }>(`/operations/bins/${id}/delivery`, { method: 'PATCH', body: JSON.stringify(data) }),
  recordBinEvacuation: (tracking_id: string, note = '', resident_email = '') => request<{ event_id: number; tracking_id: string; status: string; evacuated_at: string; unit_code: string; resident: { full_name: string; email: string } }>('/operations/bin-evacuations', { method: 'POST', body: JSON.stringify({ tracking_id, note, resident_email }) }),
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
  deviceReadings: (id: string) => request<{ readings: SensorReading[] }>(`/admin/devices/${encodeURIComponent(id)}/readings`),
  simulateDeviceReading: (id: string, distance_cm: number) => request<{ id: number; device_id: string; measured_at: string; fill_percent: number; level: string; simulated: boolean }>(`/admin/devices/${encodeURIComponent(id)}/simulate-reading`, { method: 'POST', body: JSON.stringify({ distance_cm }) }),
  adminContainers: () => request<{ containers: ContainerRecord[] }>('/admin/containers'),
  createDevice: (data: { id?: string; container_id: string; simulated: boolean }) => request<{ id: string; token: string; warning: string; simulated: boolean }>('/admin/devices', { method: 'POST', body: JSON.stringify(data) }),
  createInspection: (data: { container_id: string; condition: string; notes: string }) => request<{ id: number }>('/operations/inspections', { method: 'POST', body: JSON.stringify(data) }),
  updateDevice: (id: string, data: { enabled?: boolean; rotate_token?: boolean }) => request<{ id: string; enabled?: boolean; token?: string; warning?: string }>(`/admin/devices/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(data) }),
  auditLogs: () => request<{ audit_logs: { id: number; actor: string; action: string; resource_type: string; resource_id: string; details: unknown; created_at: string }[] }>('/admin/audit-logs'),
  adminActivity: (offset = 0) => request<{ activity: { created_at: string; actor_role: string; actor: string; actor_email: string; action: string; resource_type: string; resource_id: string; summary: string }[]; has_more: boolean; next_offset: number }>('/admin/activity?limit=250&offset=' + offset),
  settings: () => request<{ settings: Record<string, number> }>('/admin/settings'),
  updateSettings: (data: Record<string, number>) => request<{ updated: boolean }>('/admin/settings', { method: 'PATCH', body: JSON.stringify(data) }),
  areaFees: () => request<{ area_fees: { id: number; area_id: number | null; area_name: string; locality: string | null; city: string | null; state: string | null; resolved: boolean; amount_kobo: number; updated_at: string }[] }>('/admin/area-fees'),
  serviceAreasAdmin: () => request<{ service_areas: ServiceArea[] }>('/admin/service-areas'),
  createServiceArea: (data: { locality: string; city: string; state: string }) => request<{ service_area: ServiceArea }>('/admin/service-areas', { method: 'POST', body: JSON.stringify(data) }),
  updateServiceArea: (id: number, active: boolean) => request<{ service_area: ServiceArea }>(`/admin/service-areas/${id}`, { method: 'PATCH', body: JSON.stringify({ active }) }),
  saveAreaFee: (area_id: number, amount_kobo: number, legacy_fee_id?: number) => request<{ id: number }>('/admin/area-fees', { method: 'PUT', body: JSON.stringify({ area_id, amount_kobo, legacy_fee_id }) }),
}
