package main

import (
	"fmt"
	"net/http"
	"strconv"
)

func (s *server) adminActivity(w http.ResponseWriter, r *http.Request) {
	limit := 250
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "Activity limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	offset := 0
	if value := r.URL.Query().Get("offset"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset", "Activity offset must be zero or greater")
			return
		}
		offset = parsed
	}
	rows, err := s.db.Query(r.Context(), `
		WITH activity AS (
			SELECT u.created_at, 'resident'::text AS actor_role, u.full_name AS actor,
				u.email AS actor_email, 'account_registered'::text AS action,
				'resident'::text AS resource_type, u.id::text AS resource_id,
				'Resident account created'::text AS summary
			FROM users u WHERE u.role='citizen'
			UNION ALL
			SELECT l.created_at, 'resident', u.full_name, u.email, 'address_added',
				'resident_location', l.id::text,
				concat('Address added: ',l.label,' · ',l.address_line1,', ',l.locality,', ',l.city,', ',l.state)
			FROM resident_locations l JOIN users u ON u.id=l.resident_id
			UNION ALL
			SELECT c.created_at, 'resident', u.full_name, u.email, 'complaint_submitted',
				'complaint', c.reference::text,
				concat('Complaint submitted · ',replace(c.category,'_',' '),' · ',c.status)
			FROM complaints c JOIN users u ON u.id=c.citizen_id
			UNION ALL
			SELECT p.created_at, 'resident', u.full_name, u.email, 'payment_created',
				'payment', p.reference::text,
				concat('Waste fee payment · ',p.amount_kobo,' kobo · ',p.status,' · ',p.mode)
			FROM payments p JOIN users u ON u.id=p.citizen_id
			UNION ALL
			SELECT p.created_at, 'resident', u.full_name, u.email, 'property_registered',
				'registered_property', p.id::text,
				concat('Property registration submitted · ',p.property_type,' · ',p.status)
			FROM registered_properties p JOIN users u ON u.id=p.registered_by WHERE u.role='citizen'
			UNION ALL
			SELECT q.created_at, 'resident', u.full_name, u.email, 'property_unit_requested',
				'property_unit_request', q.id::text,
				concat('Unit access requested · ',q.unit_code,' · ',q.status)
			FROM property_unit_requests q JOIN users u ON u.id=q.requester_id
			UNION ALL
			SELECT a.created_at, coalesce(u.role,'system'), coalesce(u.full_name,'System'),
				coalesce(u.email,''), a.action, a.resource_type, a.resource_id,
				concat('Staff action · ',replace(a.action,'_',' '),' · ',a.resource_type,' ',a.resource_id)
			FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_id
			UNION ALL
			SELECT r.received_at, 'system', 'Sensor service', '', 'sensor_reading_received',
				'device', d.id, concat(c.label,' · ',r.fill_percent,'% full · ',r.level,
					CASE WHEN r.is_simulated THEN ' · simulated' ELSE ' · hardware' END)
			FROM sensor_readings r JOIN devices d ON d.id=r.device_id JOIN containers c ON c.id=d.container_id
		)
		SELECT created_at::text,actor_role,actor,actor_email,action,resource_type,resource_id,summary
		FROM activity ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit+1, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "activity_unavailable", "Organization activity could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var created, role, actor, email, action, resourceType, resourceID, summary string
		if err := rows.Scan(&created, &role, &actor, &email, &action, &resourceType, &resourceID, &summary); err != nil {
			writeError(w, http.StatusInternalServerError, "activity_unavailable", "Organization activity could not be loaded")
			return
		}
		items = append(items, map[string]any{
			"created_at": created, "actor_role": role, "actor": actor, "actor_email": email,
			"action": action, "resource_type": resourceType, "resource_id": resourceID, "summary": summary,
		})
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "activity_unavailable", "Organization activity could not be loaded")
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": items, "has_more": hasMore, "next_offset": offset + len(items), "page": fmt.Sprintf("%d-%d", offset+1, offset+len(items))})
}
