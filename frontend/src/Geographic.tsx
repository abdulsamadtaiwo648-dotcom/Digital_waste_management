import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api } from './api'
import type { BinEvacuationEvent, ResidentLocation, ServiceArea } from './api'
import { GoogleAddressPicker } from './GoogleAddressPicker'

function areaLabel(area: ServiceArea) {
  return `${area.locality}, ${area.city}, ${area.state}`
}

function money(kobo: number) {
  return new Intl.NumberFormat('en-NG', { style: 'currency', currency: 'NGN' }).format(kobo / 100)
}

function statusLabel(value: string) {
  return value.replaceAll('_', ' ').replace(/^./, character => character.toUpperCase())
}

export function BinEvacuationHistory({ binID }: { binID: number }) {
  const [events, setEvents] = useState<BinEvacuationEvent[]>([])
  const [nextBeforeID, setNextBeforeID] = useState<number | null>(null)
  const [hasMore, setHasMore] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function load(beforeID?: number) {
    setBusy(true)
    setError('')
    try {
      const result = await api.binEvacuationHistory(binID, beforeID)
      setEvents(current => beforeID ? [...current, ...result.events] : result.events)
      setNextBeforeID(result.next_before_id)
      setHasMore(result.has_more)
      setLoaded(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Collection history could not be loaded')
    } finally {
      setBusy(false)
    }
  }

  return <details className="evacuation-history" onToggle={event => {
    if (event.currentTarget.open && !loaded && !busy) void load()
  }}>
    <summary>Collection history</summary>
    {busy && events.length === 0 ? <p>Loading collection history…</p> : null}
    {error && <div><p className="form-error" role="alert">{error}</p><button type="button" className="link-button" disabled={busy} onClick={() => void load()}>Try again</button></div>}
    {!busy && !error && events.length === 0 && loaded && <p>No collections recorded yet.</p>}
    {events.length > 0 && <ol>{events.map(item => <li key={item.id}><time>{new Date(item.evacuated_at).toLocaleString()}</time><span>{item.staff_name || 'Service team'}</span>{item.note && <p>{item.note}</p>}</li>)}</ol>}
    {hasMore && <button type="button" className="link-button" disabled={busy} onClick={() => nextBeforeID && void load(nextBeforeID)}>{busy ? 'Loading…' : 'Load older collections'}</button>}
  </details>
}

export function ManagedLocationsPanel() {
  const [items, setItems] = useState<ResidentLocation[]>([])
  const [areas, setAreas] = useState<ServiceArea[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [editingId, setEditingId] = useState<number | null>(null)
  const [selectedArea, setSelectedArea] = useState('')
  const [addressLine1, setAddressLine1] = useState('')
  const [postalCode, setPostalCode] = useState('')
  const [coordinates, setCoordinates] = useState<{ latitude: number; longitude: number } | null>(null)

  const load = async () => {
    const [locations, serviceAreas] = await Promise.all([api.locations(), api.serviceAreas()])
    setItems(locations.locations)
    setAreas(serviceAreas.service_areas)
  }
  useEffect(() => { load().catch(e => setError(e.message)) }, [])

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const d = new FormData(form)
    setBusy(true); setError('')
    try {
      await api.createLocation({ area_id: Number(d.get('area_id')), label: String(d.get('label')), address_line1: String(d.get('address_line1')), address_line2: String(d.get('address_line2')), postal_code: String(d.get('postal_code')), ...(coordinates ?? {}) })
      form.reset()
      setAddressLine1(''); setPostalCode(''); setCoordinates(null)
      await load()
    } catch (e) { setError(e instanceof Error ? e.message : 'Address could not be saved') }
    finally { setBusy(false) }
  }

  async function saveArea(location: ResidentLocation) {
    setBusy(true); setError('')
    try {
      await api.updateLocation(location.id, { area_id: Number(selectedArea), label: location.label, address_line1: location.address_line1, address_line2: location.address_line2, postal_code: location.postal_code })
      setEditingId(null)
      await load()
    } catch (e) { setError(e instanceof Error ? e.message : 'Address area could not be updated') }
    finally { setBusy(false) }
  }

  async function remove(id: number) {
    try { await api.deleteLocation(id); await load() }
    catch (e) { setError(e instanceof Error ? e.message : 'Address could not be removed') }
  }

  return <>
    <section className="panel">
      <div className="panel-heading"><div><h2>Homes and service locations</h2><p>Each address is attached to a managed service area for fee selection.</p></div></div>
      {items.map(x => <div className="location-row" key={x.id}><div><strong>{x.label}</strong><p>{[x.address_line1, x.address_line2, x.locality, x.city, x.state, x.postal_code].filter(Boolean).join(', ')}</p>{x.property_registered && <p className="muted">Registered address details are locked.{x.property_relationship === 'tenant' ? ' If you move, choose “I moved out” to end your access, then add the new address.' : ' Contact management if this record needs correction or transfer.'}</p>}{!x.area_id && <p className="form-error">Service area unresolved. Select the correct area to enable fee lookup.</p>}{editingId === x.id && <div className="form-stack"><label className="field"><span>Verified service area</span><select value={selectedArea} onChange={e => setSelectedArea(e.target.value)} required><option value="">Choose area</option>{areas.map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select></label><button className="button" disabled={busy || !selectedArea} onClick={() => saveArea(x)}>Save area</button></div>}</div><div>{editingId !== x.id && !x.property_registered && <button className="link-button" onClick={() => { setEditingId(x.id); setSelectedArea(x.area_id ? String(x.area_id) : '') }}>Set area</button>}{(!x.property_registered || x.property_relationship === 'tenant') && <button className="link-button" onClick={() => remove(x.id)}>{x.property_registered ? 'I moved out' : 'Remove'}</button>}</div></div>)}
      {items.length === 0 && <p className="empty-inline">No service addresses saved yet.</p>}
    </section>
    <section className="panel form-panel">
      <h2>Add a home or business</h2>
      <form className="form-grid" onSubmit={submit}>
        <div className="address-map-field"><GoogleAddressPicker onAddress={result => { setAddressLine1(result.addressLine1); setPostalCode(result.postalCode); setCoordinates({ latitude: result.latitude, longitude: result.longitude }) }}/></div>
        <label className="field"><span>Property label</span><input name="label" placeholder="Home, Shop, Workshop" required maxLength={80}/></label>
        <label className="field"><span>Street address</span><input name="address_line1" autoComplete="address-line1" required maxLength={180} value={addressLine1} onChange={e => setAddressLine1(e.target.value)}/></label>
        <label className="field"><span>Apartment or suite (optional)</span><input name="address_line2" autoComplete="address-line2" maxLength={180}/></label>
        <label className="field"><span>Service area</span><select name="area_id" required defaultValue=""><option value="" disabled>{areas.length ? 'Choose locality, city and state' : 'No active service areas available'}</option>{areas.map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select></label>
        <label className="field"><span>Postal code (optional)</span><input name="postal_code" autoComplete="postal-code" maxLength={24} value={postalCode} onChange={e => setPostalCode(e.target.value)}/></label>
        <p className="muted form-hint">Review the suggested street address, then choose the correct managed service area. The map does not determine service-area boundaries.</p>
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="button" disabled={busy || areas.length === 0}>{busy ? 'Saving…' : 'Save address'}</button>
      </form>
    </section>
  </>
}

export function PropertyRegistrationPanel() {
  const [locations, setLocations] = useState<ResidentLocation[]>([])
  const [properties, setProperties] = useState<Awaited<ReturnType<typeof api.properties>>['properties']>([])
  const [unitRequests, setUnitRequests] = useState<Awaited<ReturnType<typeof api.residentPropertyUnitRequests>>['requests']>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const load = async () => { const [l,p,r] = await Promise.all([api.locations(),api.properties(),api.residentPropertyUnitRequests()]); setLocations(l.locations); setProperties(p.properties); setUnitRequests(r.requests) }
  useEffect(() => {
    const refresh = () => { if (document.visibilityState === 'visible') load().catch(e => setError(e.message)) }
    refresh()
    const timer = window.setInterval(refresh, 30000)
    return () => window.clearInterval(timer)
  }, [])

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const d = new FormData(form)
    const scope = String(d.get('registration_scope'))
    const codes = String(d.get('unit_codes')).split(',').map(x => x.trim()).filter(Boolean).map(entry => {
      const [code, ...floor] = entry.split('|')
      return { code: code.trim(), floor_label: floor.join('|').trim() }
    })
    setBusy(true); setError(''); setMessage('')
    try {
      await api.registerProperty({
        location_id: Number(d.get('location_id')),
        property_type: String(d.get('property_type')),
        structure_type: String(d.get('structure_type')),
        floors: Number(d.get('floors')),
        registration_scope: scope,
        registrant_role: String(d.get('registrant_role')),
        units: codes.map(unit => ({ ...unit, unit_type: String(d.get('unit_type')), service_scope: String(d.get('service_scope')) })),
      })
      form.reset(); await load(); setMessage('Property registration submitted. A staff member will arrange the tagged bin handover; delivery is what confirms service at the address.')
    } catch (e) { setError(e instanceof Error ? e.message : 'Property could not be registered') }
    finally { setBusy(false) }
  }

  async function requestUnit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const form = e.currentTarget; const d = new FormData(form); setBusy(true); setError(''); setMessage('')
    try {
      await api.requestPropertyUnit({ location_id: Number(d.get('location_id')), code: String(d.get('code')), unit_type: String(d.get('unit_type')), floor_label: String(d.get('floor_label')), service_scope: String(d.get('service_scope')), relationship: String(d.get('relationship')) })
      form.reset(); await load(); setMessage('Unit request submitted. Staff will review it against the existing property record before linking your account.')
    } catch (e) { setError(e instanceof Error ? e.message : 'Unit request could not be submitted') }
    finally { setBusy(false) }
  }

  return <section className="panel form-panel">
    <div className="panel-heading"><div><h2>Register a property</h2><p>One parent property can contain separate flats, floors, or shops. The physical delivery record will confirm the service location.</p></div></div>
    <form className="form-grid" onSubmit={submit}>
      <label className="field"><span>1. Property type</span><select name="property_type" required defaultValue=""><option value="" disabled>Choose property type</option><option value="residential">Residential</option><option value="commercial">Commercial</option><option value="mixed_use">Mixed use</option><option value="other">Other</option></select></label>
      <label className="field"><span>Building structure</span><select name="structure_type" required defaultValue=""><option value="" disabled>Choose structure</option><option value="bungalow">Bungalow</option><option value="storey_building">Storey building</option><option value="apartment_block">Apartment block</option><option value="shop_complex">Shop complex</option><option value="mixed_use">Mixed use</option><option value="other">Other</option></select></label>
      <label className="field"><span>Number of floors</span><input name="floors" type="number" min="1" max="100" defaultValue="1" required/></label>
      <label className="field"><span>2. Registering</span><select name="registration_scope" required defaultValue=""><option value="" disabled>Choose coverage</option><option value="whole_property">The whole property</option><option value="single_unit">One unit only</option></select></label>
      <label className="field"><span>Your role</span><select name="registrant_role" required defaultValue=""><option value="" disabled>Choose your role</option><option value="owner">Owner</option><option value="tenant">Tenant</option><option value="caretaker">Caretaker</option><option value="agent">Agent</option></select></label>
      <label className="field"><span>Saved property address</span><select name="location_id" required defaultValue=""><option value="" disabled>{locations.length ? 'Choose address' : 'Add a service address above first'}</option>{locations.map(x => <option key={x.id} value={x.id}>{x.label} — {x.address_line1}, {x.locality}, {x.city}</option>)}</select></label>
      <label className="field"><span>Unit type</span><select name="unit_type" required defaultValue="residential"><option value="residential">Flat or residence</option><option value="shop">Shop</option><option value="office">Office</option><option value="shared">Shared service area</option><option value="other">Other</option></select></label>
      <label className="field"><span>Bin arrangement</span><select name="service_scope" required defaultValue="individual_bin"><option value="individual_bin">Separate bin for each unit</option><option value="shared_bin">One shared bin for these units</option></select></label>
      <label className="field"><span>Unit name(s)</span><input name="unit_codes" required placeholder="Downstairs Flat | Ground floor, Upstairs Flat | First floor"/><small>For the whole property, separate units with commas. Add a floor after |. For one unit, enter one name.</small></label>
      <p className="muted form-hint">Choose the structure before specifying coverage. Do not create a second property record for another unit in a property already registered; ask the registered caretaker or contact staff for help.</p>
      {error && <p className="form-error" role="alert">{error}</p>}{message && <p className="success-message">{message}</p>}
      <button className="button" disabled={busy || locations.length === 0}>{busy ? 'Submitting…' : 'Submit property registration'}</button>
    </form>
    <div className="panel-subsection"><h3>Join an already registered property</h3><p className="muted">If the building is already registered by its owner or caretaker, request a separate unit here. Staff must approve it before it is linked to your account.</p>
      <form className="form-grid" onSubmit={requestUnit}><label className="field"><span>Saved address</span><select name="location_id" required defaultValue=""><option value="" disabled>Choose the matching address</option>{locations.map(x=><option key={x.id} value={x.id}>{x.label} — {x.address_line1}, {x.locality}, {x.city}</option>)}</select></label><label className="field"><span>Unit name</span><input name="code" required maxLength={80} placeholder="Upstairs flat or Shop C"/></label><label className="field"><span>Floor</span><input name="floor_label" maxLength={80} placeholder="First floor"/></label><label className="field"><span>Unit type</span><select name="unit_type"><option value="residential">Flat or residence</option><option value="shop">Shop</option><option value="office">Office</option><option value="other">Other</option></select></label><label className="field"><span>Bin arrangement</span><select name="service_scope"><option value="individual_bin">Individual bin</option><option value="shared_bin">Shared property bin</option></select></label><label className="field"><span>Your role</span><select name="relationship"><option value="tenant">Tenant</option><option value="owner">Owner</option><option value="caretaker">Caretaker</option><option value="agent">Agent</option></select></label><button className="button" disabled={busy || locations.length === 0}>{busy ? 'Submitting…' : 'Request unit access'}</button></form>
    </div>
    {unitRequests.length > 0 && <div className="panel-subsection"><h3>Unit access requests</h3>{unitRequests.map(request=><article className="location-row" key={request.id}><div><strong>{request.code}{request.floor_label?` · ${request.floor_label}`:''}</strong><p>{statusLabel(request.status)}{request.review_reason?` · ${request.review_reason}`:''}</p></div></article>)}</div>}
    {properties.length > 0 && <div className="panel-subsection"><h3>Your registered properties</h3>{properties.map(p => <article className="location-row" key={p.id}><div><strong>{p.address}</strong><p>{p.area} · <span className={`status status-${p.review_status}`}>{statusLabel(p.review_status)}</span>{p.review_reason&&<span> · {p.review_reason}</span>}</p><p>Units: {p.units.map(u => u.code).join(', ')}</p>{p.bins.length>0&&<div><strong>Bin profiles and collection activity</strong>{p.bins.map(b=><div key={b.id}><p>{b.tracking_id} · {b.size_litres} L{b.unit_code?` · ${b.unit_code}`:''} · {b.status.replaceAll('_',' ')}{b.assigned_to_you?' · Assigned to your account':''}{b.registered_at?` · Registered ${new Date(b.registered_at).toLocaleDateString()}`:''}{b.delivered_at?` · Delivered ${new Date(b.delivered_at).toLocaleDateString()}`:''}{b.last_evacuated_at?` · Last evacuated ${new Date(b.last_evacuated_at).toLocaleString()}${b.evacuated_by?` by ${b.evacuated_by}`:''}`:' · No evacuation recorded yet'}{b.delivery_note?` · ${b.delivery_note}`:''}</p><BinEvacuationHistory binID={b.id}/></div>)}</div>}</div></article>)}</div>}
  </section>
}

export function ManagedAreaFees() {
  const [areas, setAreas] = useState<ServiceArea[]>([])
  const [fees, setFees] = useState<Awaited<ReturnType<typeof api.areaFees>>['area_fees']>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [legacyArea, setLegacyArea] = useState<Record<number, string>>({})
  const [baseFeePreview, setBaseFeePreview] = useState('')

  const load = async () => {
    const [a, f] = await Promise.all([api.serviceAreasAdmin(), api.areaFees()])
    setAreas(a.service_areas); setFees(f.area_fees)
  }
  useEffect(() => { load().catch(e => setError(e.message)) }, [])

  async function createArea(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const form = e.currentTarget; const d = new FormData(form); setBusy(true); setError(''); setMessage('')
    try { await api.createServiceArea({ locality: String(d.get('locality')), city: String(d.get('city')), state: String(d.get('state')) }); form.reset(); await load(); setMessage('Service area created.') }
    catch (e) { setError(e instanceof Error ? e.message : 'Service area could not be created') }
    finally { setBusy(false) }
  }

  async function saveFee(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const d = new FormData(e.currentTarget); setBusy(true); setError(''); setMessage('')
    try { await api.saveAreaFee(Number(d.get('area_id')), Math.round(Number(d.get('amount_naira')) * 100)); await load(); setMessage('Area fee saved in kobo.') }
    catch (e) { setError(e instanceof Error ? e.message : 'Area fee could not be saved') }
    finally { setBusy(false) }
  }

  async function reconcile(fee: typeof fees[number]) {
    const areaID = Number(legacyArea[fee.id]); if (!areaID) return
    setBusy(true); setError(''); setMessage('')
    try { await api.saveAreaFee(areaID, fee.amount_kobo, fee.id); await load(); setMessage('Legacy fee attached to the selected area without changing its amount.') }
    catch (e) { setError(e instanceof Error ? e.message : 'Legacy fee could not be reconciled') }
    finally { setBusy(false) }
  }

  async function toggle(area: ServiceArea) {
    setBusy(true); setError('')
    try { await api.updateServiceArea(area.id, !area.active); await load() }
    catch (e) { setError(e instanceof Error ? e.message : 'Service area could not be updated') }
    finally { setBusy(false) }
  }

  return <>
    <section className="panel form-panel"><h2>Managed service areas</h2><p className="muted">Area identity uses locality, city and state together. Duplicate locality names in different cities remain distinct.</p>
      <form className="form-grid" onSubmit={createArea}><label className="field"><span>Locality</span><input name="locality" required maxLength={120}/></label><label className="field"><span>City or town</span><input name="city" required maxLength={120}/></label><label className="field"><span>State</span><input name="state" required maxLength={120}/></label><button className="button" disabled={busy}>Create service area</button></form>
    </section>
    <section className="panel"><h2>Service areas</h2>{areas.length === 0 ? <p className="empty-inline">No managed service areas have been added.</p> : <div className="table-wrap"><table><thead><tr><th>Service area</th><th>Status</th><th/></tr></thead><tbody>{areas.map(a => <tr key={a.id}><td>{areaLabel(a)}</td><td>{a.active ? 'Active' : 'Inactive'}</td><td><button className="link-button" disabled={busy} onClick={() => toggle(a)}>{a.active ? 'Deactivate' : 'Activate'}</button></td></tr>)}</tbody></table></div>}</section>
    <section className="panel settings-panel"><h2>Waste fee by service area</h2><p className="muted">Set one base fee per area for a 60 L bin. Each delivered bin assigned to a resident is charged in proportion to its capacity: 120 L costs twice the base and 240 L costs four times the base. If a resident has multiple bins, their charge is the sum of those bin prices; there is no additional area fee.</p>
      <form className="form-stack" onSubmit={saveFee}><label className="field"><span>Service area</span><select name="area_id" required defaultValue=""><option value="" disabled>Choose area</option>{areas.filter(a => a.active).map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select></label><label className="field"><span>Base fee for 60 L (NGN)</span><input name="amount_naira" type="number" min="0" step="0.01" required value={baseFeePreview} onChange={e => setBaseFeePreview(e.target.value)}/></label><div className="table-wrap"><table><thead><tr><th>Bin capacity</th><th>Calculated fee</th></tr></thead><tbody>{[60, 120, 240, 660, 1100].map(size => <tr key={size}><td>{size} L</td><td>{baseFeePreview && Number.isFinite(Number(baseFeePreview)) ? money(Math.ceil(Number(baseFeePreview) * 100 * size / 60)) : 'Enter the 60 L base fee'}</td></tr>)}</tbody></table></div><button className="button" disabled={busy || !areas.some(a => a.active)}>Save 60 L base fee</button></form>
    </section>
    <section className="panel"><h2>Fee records</h2>{fees.length === 0 ? <p className="empty-inline">No area fees have been configured.</p> : <div className="table-wrap"><table><thead><tr><th>Area</th><th>60 L base fee</th><th>Last updated</th></tr></thead><tbody>{fees.map(f => <tr key={f.id}><td>{f.resolved ? `${f.locality}, ${f.city}, ${f.state}` : <><strong>{f.area_name}</strong><p className="form-error">Unresolved legacy fee. Confirm its city and state before assigning.</p><select aria-label={`Assign legacy fee ${f.area_name} to service area`} value={legacyArea[f.id] || ''} onChange={e => setLegacyArea({ ...legacyArea, [f.id]: e.target.value })}><option value="">Choose area</option>{areas.filter(a => a.active).map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select><button className="link-button" disabled={busy || !legacyArea[f.id]} onClick={() => reconcile(f)}>Assign without changing fee</button></>}</td><td>{money(f.amount_kobo)}</td><td>{new Date(f.updated_at).toLocaleString()}</td></tr>)}</tbody></table></div>}</section>
    {error && <p className="form-error" role="alert">{error}</p>}{message && <p className="success-message">{message}</p>}
  </>
}
