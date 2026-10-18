import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api } from './api'
import type { DeviceRecord, SensorReading } from './api'

type ContainerOption = { id: string; label: string }

export function OperationsContainerConsole() {
  const [containers, setContainers] = useState<Awaited<ReturnType<typeof api.containers>>['containers']>([])
  const [history, setHistory] = useState<Record<string, SensorReading[]>>({})
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)

  async function load() {
    try {
      const result = await api.containers()
      setContainers(result.containers)
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Public containers could not be loaded')
    }
  }

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => { void load() }, 30000)
    return () => window.clearInterval(timer)
  }, [])

  async function register(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const data = new FormData(form)
    setBusy(true)
    setError('')
    setMessage('')
    try {
      const result = await api.createContainer({
        label: String(data.get('label')),
        service_area: String(data.get('service_area')),
        usable_depth_cm: Number(data.get('depth')),
        minimum_distance_cm: Number(data.get('minimum')),
        installation_offset_cm: Number(data.get('offset')),
      })
      setMessage(`Container ${result.id} registered. Connect a sensor device from the admin workspace.`)
      form.reset()
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Container could not be registered')
    } finally {
      setBusy(false)
    }
  }

  async function showHistory(id: string) {
    try {
      const result = await api.containerReadings(id)
      setHistory(current => ({ ...current, [id]: result.readings }))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Container readings could not be loaded')
    }
  }

  return <>
    <header className="content-heading"><div><p className="eyebrow">Operations workspace</p><h1>Public containers</h1><p className="muted">Monitor fill level, sensor freshness and recent reading history for public collection points.</p><p className="form-hint">When multiple enabled sensors report to one container, the displayed fill level is the highest recent calculated fill estimate.</p></div><span className="status">Refreshes every 30 seconds</span></header>
    {error && <p className="form-error" role="alert">{error}</p>}
    {message && <p className="success-message" role="status">{message}</p>}
    <section className="panel">
      <div className="panel-heading"><div><h2>Register a public container</h2><p>Enter the physical container dimensions used to calculate sensor fill level.</p></div></div>
      <form className="inline-form" onSubmit={register}>
        <label className="field"><span>Container name</span><input name="label" required minLength={2} maxLength={120} placeholder="Central market skip" /></label>
        <label className="field"><span>Service area</span><input name="service_area" required minLength={2} maxLength={120} placeholder="Area or route name" /></label>
        <label className="field"><span>Usable depth (cm)</span><input name="depth" type="number" min="1" step="0.1" required /></label>
        <label className="field"><span>Minimum sensor distance (cm)</span><input name="minimum" type="number" min="0" step="0.1" defaultValue="2" required /></label>
        <label className="field"><span>Installation offset (cm)</span><input name="offset" type="number" min="0" step="0.1" defaultValue="0" required /></label>
        <button className="button" disabled={busy}>{busy ? 'Saving…' : 'Register container'}</button>
      </form>
    </section>
    <section className="panel">
      <div className="panel-heading"><div><h2>Container monitoring</h2><p>No reading means no sensor signal has been received yet.</p></div><button className="button button-small button-outline" type="button" onClick={() => void load()}>Refresh</button></div>
      {containers.length === 0 ? <p className="empty-inline">No public containers registered.</p> : <div className="table-wrap"><table><thead><tr><th>Container</th><th>Area</th><th>Sensors</th><th>Fill</th><th>Level</th><th>Last signal</th><th>Sensor details</th></tr></thead><tbody>{containers.map(container => <tr key={container.id}>
        <td><strong>{container.label}</strong><br/><small>{container.id}</small></td>
        <td>{container.service_area}</td>
        <td>{container.active_sensor_count} active{container.active_sensor_count < 2 ? ' · pair incomplete' : ''}</td>
        <td>{container.latest_fill_percent == null ? 'No reading received' : <><div className="fill-value">{container.latest_fill_percent}%</div><div className="fill-track"><span style={{ width: `${Math.max(0, Math.min(100, container.latest_fill_percent))}%` }} /></div><small>{container.usable_depth_cm} cm usable depth</small></>}</td>
        <td>{container.latest_level ? <span className={`status status-${container.latest_level}`}>{container.latest_level}</span> : <span className="status">Waiting</span>}</td>
        <td>{container.last_reading_at ? new Date(container.last_reading_at).toLocaleString() : 'No connection yet'}</td>
        <td><button className="link-button" type="button" onClick={() => void showHistory(container.id)}>{history[container.id] ? 'Refresh readings' : 'View readings'}</button></td>
      </tr>)}</tbody></table></div>}
      {containers.filter(c => history[c.id]).map(container => <div className="device-history" key={`${container.id}-readings`}><h3>{container.label} · Reading history</h3>{history[container.id].length === 0 ? <p className="empty-inline">No sensor signal has been received for this container.</p> : <ReadingTable readings={history[container.id]} />}</div>)}
    </section>
  </>
}

function ReadingTable({ readings }: { readings: SensorReading[] }) {
  return <div className="table-wrap"><table><thead><tr><th>Measured</th><th>Received</th><th>Distance</th><th>Fill</th><th>Level</th><th>Source</th></tr></thead><tbody>{readings.map(item => <tr key={item.id}><td>{new Date(item.measured_at).toLocaleString()}</td><td>{new Date(item.received_at).toLocaleString()}</td><td>{item.distance_cm} cm</td><td>{item.fill_percent}%</td><td><span className={`status status-${item.level}`}>{item.level}</span></td><td>{item.simulated ? 'Simulator' : 'Hardware'}</td></tr>)}</tbody></table></div>
}

export function AdminDevicesConsole() {
  const [devices, setDevices] = useState<DeviceRecord[]>([])
  const [containers, setContainers] = useState<ContainerOption[]>([])
  const [readings, setReadings] = useState<Record<string, SensorReading[]>>({})
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState('')

  async function load() {
    try {
      const [deviceResult, containerResult] = await Promise.all([api.devices(), api.adminContainers()])
      setDevices(deviceResult.devices)
      setContainers(containerResult.containers.map(c => ({ id: c.id, label: c.label })))
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Device data could not be loaded')
    }
  }

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => { void load() }, 30000)
    return () => window.clearInterval(timer)
  }, [])

  async function create(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const data = new FormData(form)
    setBusy('register')
    setError('')
    try {
      const result = await api.createDevice({
        id: String(data.get('id') || ''),
        container_id: String(data.get('container_id')),
        simulated: import.meta.env.DEV && data.get('simulated') === 'on',
      })
      setSecret(`${result.id}: ${result.token}`)
      setMessage(result.simulated ? 'Simulator device registered.' : 'Device registered.')
      form.reset()
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Device could not be registered')
    } finally {
      setBusy('')
    }
  }

  async function toggle(id: string, enabled: boolean) {
    setBusy(id)
    try {
      await api.updateDevice(id, { enabled })
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Device access could not be updated')
    } finally {
      setBusy('')
    }
  }

  async function rotate(id: string) {
    setBusy(id)
    try {
      const result = await api.updateDevice(id, { rotate_token: true })
      if (result.token) setSecret(`${id}: ${result.token}`)
      setMessage('Device credential rotated. Copy the new credential now.')
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Device credential could not be rotated')
    } finally {
      setBusy('')
    }
  }

  async function simulate(device: DeviceRecord, targetFill: number) {
    const correctedDistance = Math.max(device.minimum_distance_cm, device.container_depth_cm * (1 - targetFill / 100))
    const distance = Number((correctedDistance + device.installation_offset_cm).toFixed(2))
    setBusy(device.id)
    setError('')
    try {
      const result = await api.simulateDeviceReading(device.id, distance)
      setMessage(`${device.container_label}: simulated ${result.fill_percent}% fill (${result.level}).`)
      await Promise.all([load(), loadHistory(device.id)])
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Test reading could not be generated')
    } finally {
      setBusy('')
    }
  }

  async function loadHistory(id: string) {
    try {
      const result = await api.deviceReadings(id)
      setReadings(current => ({ ...current, [id]: result.readings }))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Reading history could not be loaded')
    }
  }

  async function createForm(e: FormEvent<HTMLFormElement>) { await create(e) }

  return <>
    <header className="content-heading">
      <div><p className="eyebrow">System administration</p><h1>Public container devices</h1><p className="muted">Register sensors, inspect readings and verify alert behavior before physical hardware is connected.</p></div>
      <span className="status">Refreshes every 30 seconds</span>
    </header>
    {error && <p className="form-error" role="alert">{error}</p>}
    {message && <p className="success-message" role="status">{message}</p>}
    {secret && <section className="secret-panel"><strong>Copy this device credential now. It is shown only once.</strong><code>{secret}</code><button className="link-button" type="button" onClick={() => setSecret('')}>Dismiss</button></section>}

    <section className="panel">
      <div className="panel-heading"><div><h2>Register a device</h2><p>Attach both opposite-side sensor devices to the same public container. Operations uses the highest recent calculated fill estimate. Use simulator mode until physical sensors are installed.</p></div></div>
      <form className="inline-form" onSubmit={createForm}>
        <label className="field"><span>Device ID <small>(optional)</small></span><input name="id" maxLength={50} placeholder="Generated automatically" /></label>
        <label className="field"><span>Public container</span><select name="container_id" required defaultValue=""><option value="" disabled>Choose a container</option>{containers.map(c => <option key={c.id} value={c.id}>{c.label} · {c.id}</option>)}</select></label>
        {import.meta.env.DEV && <label className="check-field"><input type="checkbox" name="simulated" defaultChecked /><span>Development simulator</span></label>}
        <button className="button" disabled={busy === 'register'}>{busy === 'register' ? 'Registering…' : 'Register device'}</button>
      </form>
      <p className="form-hint muted">Real device credentials are hashed by the server. Store the one-time token securely when hardware is provisioned.</p>
    </section>

    <section className="panel">
      <div className="panel-heading"><div><h2>Device health and readings</h2><p>Sensor readings are measurements received by the API. Simulator records are clearly labelled.</p></div><button className="button button-small button-outline" type="button" onClick={() => void load()}>Refresh</button></div>
      {devices.length === 0 ? <div className="empty-panel"><span className="empty-icon">◉</span><h2>No devices registered</h2><p>Register a simulator device and create a test reading to exercise the dashboard.</p></div> : <div className="table-wrap"><table><thead><tr><th>Device and container</th><th>Source</th><th>Latest fill</th><th>Last signal</th><th>Access</th><th>Actions</th></tr></thead><tbody>{devices.map(device => <tr key={device.id}>
        <td><strong>{device.container_label}</strong><br/><small>{device.id} · {device.container_id}</small></td>
        <td>{device.simulated ? <span className="status">Simulator</span> : <span className="status status-resolved">Hardware</span>}</td>
        <td>{device.latest_fill_percent == null ? 'No reading' : <><strong>{device.latest_fill_percent}%</strong><br/>{device.latest_level && <span className={`status status-${device.latest_level}`}>{device.latest_level}</span>}</>}</td>
        <td>{device.last_reading_at ? new Date(device.last_reading_at).toLocaleString() : 'Never connected'}</td>
        <td><label className="check-field"><input type="checkbox" checked={device.enabled} disabled={busy === device.id} onChange={e => void toggle(device.id, e.target.checked)} /><span>{device.enabled ? 'Enabled' : 'Disabled'}</span></label></td>
        <td><div className="button-row"><button className="link-button" type="button" disabled={busy === device.id} onClick={() => void rotate(device.id)}>Rotate token</button><button className="link-button" type="button" onClick={() => void loadHistory(device.id)}>{readings[device.id] ? 'Refresh history' : 'Reading history'}</button></div>
          {import.meta.env.DEV && device.simulated && device.enabled && <div className="button-row simulator-controls"><button className="button button-small button-outline" disabled={busy === device.id} type="button" onClick={() => void simulate(device, 35)}>Test normal</button><button className="button button-small button-outline" disabled={busy === device.id} type="button" onClick={() => void simulate(device, 75)}>Test warning</button><button className="button button-small" disabled={busy === device.id} type="button" onClick={() => void simulate(device, 95)}>Test critical</button></div>}
        </td>
      </tr>)}</tbody></table></div>}
      {devices.filter(d => readings[d.id]).map(device => <div className="device-history" key={`${device.id}-history`}><h3>{device.container_label} · Recent readings</h3>{readings[device.id].length === 0 ? <p className="empty-inline">No readings received yet.</p> : <div className="table-wrap"><table><thead><tr><th>Measured</th><th>Received</th><th>Distance</th><th>Fill</th><th>Level</th><th>Source</th></tr></thead><tbody>{readings[device.id].map(item => <tr key={item.id}><td>{new Date(item.measured_at).toLocaleString()}</td><td>{new Date(item.received_at).toLocaleString()}</td><td>{item.distance_cm} cm</td><td>{item.fill_percent}%</td><td><span className={`status status-${item.level}`}>{item.level}</span></td><td>{item.simulated ? 'Simulator' : 'Hardware'}</td></tr>)}</tbody></table></div>}</div>)}
    </section>
  </>
}

type OrganizationEvent = Awaited<ReturnType<typeof api.adminActivity>>['activity'][number]

export function AdminActivityFeed() {
  const [events, setEvents] = useState<OrganizationEvent[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [offset, setOffset] = useState(0)
  const [filter, setFilter] = useState('all')
  const [search, setSearch] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function load(reset = false) {
    setBusy(true)
    try {
      const result = await api.adminActivity(reset ? 0 : offset)
      setEvents(current => reset ? result.activity : [...current, ...result.activity])
      setOffset(result.next_offset)
      setHasMore(result.has_more)
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Organization activity could not be loaded')
    } finally {
      setBusy(false)
    }
  }

  useEffect(() => { void load(true) }, [])

  const visible = events.filter(event => {
    const kind = event.actor_role === 'citizen' || event.actor_role === 'resident' ? 'resident' : event.actor_role === 'system' ? 'system' : 'staff'
    const matchesFilter = filter === 'all' || filter === kind
    const text = `${event.actor} ${event.actor_email} ${event.action} ${event.summary} ${event.resource_id}`.toLowerCase()
    return matchesFilter && text.includes(search.toLowerCase())
  })

  return <>
    <header className="content-heading"><div><p className="eyebrow">System administration</p><h1>Organization activity</h1><p className="muted">Review resident service activity, staff actions and public-container sensor readings.</p></div><button className="button button-outline" type="button" disabled={busy} onClick={() => void load(true)}>Refresh activity</button></header>
    {error && <p className="form-error" role="alert">{error}</p>}
    <section className="panel">
      <div className="panel-heading"><div><h2>Activity timeline</h2><p>Events are shown from stored service records and audit logs. Secrets are excluded.</p></div></div>
      <div className="inline-form activity-filters">
        <label className="field"><span>Activity type</span><select value={filter} onChange={e => setFilter(e.target.value)}><option value="all">All activity</option><option value="resident">Resident activity</option><option value="staff">Staff activity</option><option value="system">Sensor activity</option></select></label>
        <label className="field"><span>Search loaded events</span><input value={search} onChange={e => setSearch(e.target.value)} placeholder="Name, action, record reference" /></label>
      </div>
      {visible.length === 0 ? <p className="empty-inline">{events.length ? 'No events match this filter.' : 'No activity has been recorded yet.'}</p> : <div className="table-wrap"><table><thead><tr><th>Time</th><th>Type</th><th>Actor</th><th>Activity</th><th>Record</th></tr></thead><tbody>{visible.map((event, index) => {
        const kind = event.actor_role === 'citizen' || event.actor_role === 'resident' ? 'Resident' : event.actor_role === 'system' ? 'System' : 'Staff'
        return <tr key={`${event.action}-${event.resource_type}-${event.resource_id}-${event.created_at}-${index}`}><td>{new Date(event.created_at).toLocaleString()}</td><td><span className="status">{kind}</span></td><td><strong>{event.actor}</strong>{event.actor_email && <><br/><small>{event.actor_email}</small></>}</td><td><strong>{event.action.replaceAll('_', ' ')}</strong><br/><small>{event.summary}</small></td><td>{event.resource_type} · {event.resource_id}</td></tr>
      })}</tbody></table></div>}
      {hasMore && <div className="panel-actions"><button className="button button-outline" type="button" disabled={busy} onClick={() => void load()}>{busy ? 'Loading…' : 'Load older activity'}</button></div>}
    </section>
  </>
}
