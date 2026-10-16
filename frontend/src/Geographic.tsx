import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api } from './api'
import type { ResidentLocation, ServiceArea } from './api'

function areaLabel(area: ServiceArea) {
  return `${area.locality}, ${area.city}, ${area.state}`
}

function money(kobo: number) {
  return new Intl.NumberFormat('en-NG', { style: 'currency', currency: 'NGN' }).format(kobo / 100)
}

export function ManagedLocationsPanel() {
  const [items, setItems] = useState<ResidentLocation[]>([])
  const [areas, setAreas] = useState<ServiceArea[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [editingId, setEditingId] = useState<number | null>(null)
  const [selectedArea, setSelectedArea] = useState('')

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
      await api.createLocation({ area_id: Number(d.get('area_id')), label: String(d.get('label')), address_line1: String(d.get('address_line1')), address_line2: String(d.get('address_line2')), postal_code: String(d.get('postal_code')) })
      form.reset()
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
      {items.map(x => <div className="location-row" key={x.id}><div><strong>{x.label}</strong><p>{[x.address_line1, x.address_line2, x.locality, x.city, x.state, x.postal_code].filter(Boolean).join(', ')}</p>{!x.area_id && <p className="form-error">Service area unresolved. Select the correct area to enable fee lookup.</p>}{editingId === x.id && <div className="form-stack"><label className="field"><span>Verified service area</span><select value={selectedArea} onChange={e => setSelectedArea(e.target.value)} required><option value="">Choose area</option>{areas.map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select></label><button className="button" disabled={busy || !selectedArea} onClick={() => saveArea(x)}>Save area</button></div>}</div><div>{editingId !== x.id && <button className="link-button" onClick={() => { setEditingId(x.id); setSelectedArea(x.area_id ? String(x.area_id) : '') }}>Set area</button>}<button className="link-button" onClick={() => remove(x.id)}>Remove</button></div></div>)}
      {items.length === 0 && <p className="empty-inline">No service addresses saved yet.</p>}
    </section>
    <section className="panel form-panel">
      <h2>Add a home or business</h2>
      <form className="form-grid" onSubmit={submit}>
        <label className="field"><span>Property label</span><input name="label" placeholder="Home, Shop, Workshop" required maxLength={80}/></label>
        <label className="field"><span>Street address</span><input name="address_line1" autoComplete="address-line1" required maxLength={180}/></label>
        <label className="field"><span>Apartment or suite (optional)</span><input name="address_line2" autoComplete="address-line2" maxLength={180}/></label>
        <label className="field"><span>Service area</span><select name="area_id" required defaultValue=""><option value="" disabled>{areas.length ? 'Choose locality, city and state' : 'No active service areas available'}</option>{areas.map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select></label>
        <label className="field"><span>Postal code (optional)</span><input name="postal_code" autoComplete="postal-code" maxLength={24}/></label>
        <p className="muted form-hint">Locality, city and state are filled from the selected managed area.</p>
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="button" disabled={busy || areas.length === 0}>{busy ? 'Saving…' : 'Save address'}</button>
      </form>
    </section>
  </>
}

export function ManagedAreaFees() {
  const [areas, setAreas] = useState<ServiceArea[]>([])
  const [fees, setFees] = useState<Awaited<ReturnType<typeof api.areaFees>>['area_fees']>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [legacyArea, setLegacyArea] = useState<Record<number, string>>({})

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
    <section className="panel settings-panel"><h2>Waste fees by service area</h2><p className="muted">Fees are saved in kobo and attached to a managed area ID.</p>
      <form className="form-stack" onSubmit={saveFee}><label className="field"><span>Service area</span><select name="area_id" required defaultValue=""><option value="" disabled>Choose area</option>{areas.filter(a => a.active).map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select></label><label className="field"><span>Waste fee (NGN)</span><input name="amount_naira" type="number" min="0" step="0.01" required/></label><button className="button" disabled={busy || !areas.some(a => a.active)}>Save fee</button></form>
    </section>
    <section className="panel"><h2>Fee records</h2>{fees.length === 0 ? <p className="empty-inline">No area fees have been configured.</p> : <div className="table-wrap"><table><thead><tr><th>Area</th><th>Fee</th><th>Last updated</th></tr></thead><tbody>{fees.map(f => <tr key={f.id}><td>{f.resolved ? `${f.locality}, ${f.city}, ${f.state}` : <><strong>{f.area_name}</strong><p className="form-error">Unresolved legacy fee. Confirm its city and state before assigning.</p><select aria-label={`Assign legacy fee ${f.area_name} to service area`} value={legacyArea[f.id] || ''} onChange={e => setLegacyArea({ ...legacyArea, [f.id]: e.target.value })}><option value="">Choose area</option>{areas.filter(a => a.active).map(a => <option key={a.id} value={a.id}>{areaLabel(a)}</option>)}</select><button className="link-button" disabled={busy || !legacyArea[f.id]} onClick={() => reconcile(f)}>Assign without changing fee</button></>}</td><td>{money(f.amount_kobo)}</td><td>{new Date(f.updated_at).toLocaleString()}</td></tr>)}</tbody></table></div>}</section>
    {error && <p className="form-error" role="alert">{error}</p>}{message && <p className="success-message">{message}</p>}
  </>
}
