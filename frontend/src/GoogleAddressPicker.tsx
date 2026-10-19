import { useEffect, useRef, useState } from 'react'

type Coordinates = { latitude: number; longitude: number }
type GoogleCoordinates = { lat: number; lng: number }
export type SuggestedAddress = Coordinates & { addressLine1: string; postalCode: string; formattedAddress: string }

type GoogleAddressComponent = { long_name: string; types: string[] }
type GoogleGeocodeResult = { formatted_address: string; address_components: GoogleAddressComponent[] }
type GoogleMapPoint = { lat(): number; lng(): number }
type GoogleMapEvent = { latLng?: GoogleMapPoint | null }
type GoogleMap = {
  addListener(event: string, handler: (event: GoogleMapEvent) => void): void
  panTo(position: GoogleCoordinates): void
}
type GoogleMarker = {
  addListener(event: string, handler: (event: GoogleMapEvent) => void): void
  setPosition(position: GoogleCoordinates): void
}
type GoogleMapsNamespace = {
  maps: {
    Map: new (element: HTMLElement, options: { center: GoogleCoordinates; zoom: number; mapTypeControl: boolean; streetViewControl: boolean; fullscreenControl: boolean }) => GoogleMap
    Marker: new (options: { map: GoogleMap; draggable: boolean; title: string }) => GoogleMarker
    Geocoder: new () => { geocode(request: { location: GoogleCoordinates }, callback: (results: GoogleGeocodeResult[] | null, status: string) => void): void }
    GeocoderStatus: { OK: string }
  }
}

declare global {
  interface Window { google?: GoogleMapsNamespace }
}

let mapsApiPromise: Promise<GoogleMapsNamespace> | null = null

function loadGoogleMaps(apiKey: string): Promise<GoogleMapsNamespace> {
  if (window.google?.maps) return Promise.resolve(window.google)
  if (mapsApiPromise) return mapsApiPromise
  mapsApiPromise = new Promise<GoogleMapsNamespace>((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>('script[data-dwmp-google-maps]')
    const script = existing ?? document.createElement('script')
    const loaded = () => window.google?.maps ? resolve(window.google) : reject(new Error('Google Maps did not initialize'))
    script.addEventListener('load', loaded, { once: true })
    script.addEventListener('error', () => reject(new Error('The map could not be loaded. Check the Google Maps API key and network connection.')), { once: true })
    if (!existing) {
      script.dataset.dwmpGoogleMaps = 'true'
      script.async = true
      script.src = `https://maps.googleapis.com/maps/api/js?key=${encodeURIComponent(apiKey)}&v=weekly&loading=async&region=NG`
      document.head.appendChild(script)
    }
  }).catch(error => {
    mapsApiPromise = null
    throw error
  })
  return mapsApiPromise!
}

export function GoogleAddressPicker({ onAddress }: { onAddress: (address: SuggestedAddress) => void }) {
  const mapElement = useRef<HTMLDivElement>(null)
  const map = useRef<GoogleMap | null>(null)
  const marker = useRef<GoogleMarker | null>(null)
  const geocoder = useRef<InstanceType<GoogleMapsNamespace['maps']['Geocoder']> | null>(null)
  const onAddressRef = useRef(onAddress)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const apiKey = import.meta.env.VITE_GOOGLE_MAPS_API_KEY

  useEffect(() => { onAddressRef.current = onAddress }, [onAddress])

  useEffect(() => {
    if (!apiKey || !mapElement.current) return
    let mounted = true
    loadGoogleMaps(apiKey).then(google => {
      if (!mounted || !mapElement.current) return
      const instance = new google.maps.Map(mapElement.current, {
        center: { lat: 8.5, lng: 4.55 },
        zoom: 12,
        mapTypeControl: false,
        streetViewControl: false,
        fullscreenControl: false,
      })
      map.current = instance
      marker.current = new google.maps.Marker({ map: instance, draggable: true, title: 'Drag to your home or business' })
      geocoder.current = new google.maps.Geocoder()
      const selectPoint = (event: GoogleMapEvent) => {
        if (!event.latLng) return
        const coords = { latitude: event.latLng.lat(), longitude: event.latLng.lng() }
        marker.current?.setPosition({ lat: coords.latitude, lng: coords.longitude })
        instance.panTo({ lat: coords.latitude, lng: coords.longitude })
        lookup(coords)
      }
      instance.addListener('click', selectPoint)
      marker.current.addListener('dragend', selectPoint)
    }).catch(reason => { if (mounted) setError(reason instanceof Error ? reason.message : 'Map could not be loaded') })
    return () => { mounted = false }
  }, [apiKey])

  function lookup(coords: Coordinates) {
    if (!geocoder.current) return
    setBusy(true)
    setError('')
    geocoder.current.geocode({ location: { lat: coords.latitude, lng: coords.longitude } }, (results, status) => {
      setBusy(false)
      if (status !== window.google?.maps.GeocoderStatus.OK || !results?.length) {
        setError('No street address was found for this point. Move the pin or enter the address below.')
        return
      }
      const result = results[0]
      const component = (type: string) => result.address_components.find(part => part.types.includes(type))?.long_name ?? ''
      const street = [component('street_number'), component('route')].filter(Boolean).join(' ')
      onAddressRef.current({
        addressLine1: street || component('premise') || result.formatted_address,
        postalCode: component('postal_code'),
        formattedAddress: result.formatted_address,
        ...coords,
      })
    })
  }

  function locate() {
    setError('')
    if (!navigator.geolocation) {
      setError('This browser cannot provide your current location. Click the map to place the pin instead.')
      return
    }
    setBusy(true)
    navigator.geolocation.getCurrentPosition(
      position => {
        const coords = { latitude: position.coords.latitude, longitude: position.coords.longitude }
        marker.current?.setPosition({ lat: coords.latitude, lng: coords.longitude })
        map.current?.panTo({ lat: coords.latitude, lng: coords.longitude })
        lookup(coords)
      },
      reason => {
        setBusy(false)
        setError(reason.code === reason.PERMISSION_DENIED ? 'Location access was declined. Click the map to place the pin or type the address.' : 'Could not get your location. Click the map to place the pin or type the address.')
      },
      { enableHighAccuracy: true, timeout: 12000, maximumAge: 60000 },
    )
  }

  if (!apiKey) return <div className="address-map-unavailable"><strong>Map address lookup is not configured.</strong><span>Add VITE_GOOGLE_MAPS_API_KEY to the frontend environment. You can still type the address manually.</span></div>

  return <div className="address-map-picker">
    <div className="address-map-heading"><div><strong>Find the address on the map</strong><span>Use your current location or move the pin to the property.</span></div><button type="button" className="button button-outline" onClick={locate} disabled={busy}>{busy ? 'Looking up…' : 'Use my location'}</button></div>
    <div ref={mapElement} className="address-map" aria-label="Map to choose your home or business location" />
    <p className="map-privacy-note">The selected point is sent to Google Maps to suggest an address. Check the result before saving. Your address coordinates are stored with the address.</p>
    {error && <p className="form-error" role="alert">{error}</p>}
  </div>
}
