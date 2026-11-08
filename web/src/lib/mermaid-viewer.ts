// The viewer moves an already-sanitized SVG, preserving Mermaid's scoped styles
// and fragment IDs without introducing another copy into the document.
import { get } from 'svelte/store'
import { t } from './i18n'
import './mermaid-viewer.css'

export type ViewTransform = { x: number; y: number; scale: number }
export function zoomAt(v: ViewTransform, factor: number, x: number, y: number): ViewTransform {
  const scale = Math.max(0.2, Math.min(16, v.scale * factor))
  const ratio = scale / v.scale
  return { x: x - (x - v.x) * ratio, y: y - (y - v.y) * ratio, scale }
}
let activeClose: (() => void) | undefined

export function openMermaidViewer(svg: SVGSVGElement): () => void {
  activeClose?.()
  const translate = get(t)
  const previousFocus = document.activeElement as HTMLElement | null
  const previousOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  const marker = document.createComment('mermaid-viewer-placeholder')
  svg.before(marker)
  const originalStyle = svg.getAttribute('style')
  const dialog = document.createElement('dialog')
  dialog.className = 'mermaid-viewer'
  dialog.setAttribute('aria-label', translate('chat.diagram_viewer'))
  const toolbar = document.createElement('div')
  toolbar.className = 'mermaid-viewer-toolbar'
  const stage = document.createElement('div')
  stage.className = 'mermaid-viewer-stage'
  const canvas = document.createElement('div')
  canvas.className = 'mermaid-viewer-canvas'
  stage.append(canvas)
  dialog.append(stage, toolbar)
  document.body.append(dialog)
  canvas.append(svg)
  const box = svg.getAttribute('viewBox')?.trim().split(/[ ,]+/).map(Number)
  const width = (box?.[2] || parseFloat(svg.getAttribute('width') || '') || 800)
  const height = (box?.[3] || parseFloat(svg.getAttribute('height') || '') || 600)
  svg.style.cssText = `display:block;width:${width}px;height:${height}px;max-width:none;max-height:none;`
  let view: ViewTransform = { x: 0, y: 0, scale: 1 }
  let fitScale = 1
  let closed = false
  const size = () => ({ w: stage.clientWidth || window.innerWidth, h: stage.clientHeight || window.innerHeight })
  const fit = () => {
    const { w, h } = size()
    fitScale = Math.min((w - 32) / width, (h - 120) / height, 1)
    // Work in fit-relative units so very wide diagrams can still be magnified.
    canvas.style.width = `${width}px`
    view = { x: (w - width * fitScale) / 2, y: (h - height * fitScale) / 2, scale: 1 }
  }
  const draw = () => {
    canvas.style.transform = `translate(${view.x}px,${view.y}px) scale(${view.scale * fitScale})`
  }
  const reset = () => { fit(); draw() }
  const zoom = (factor: number, x?: number, y?: number) => {
    const { w, h } = size()
    view = zoomAt(view, factor, x ?? w / 2, y ?? h / 2)
    draw()
  }
  const close = () => {
    if (closed) return
    closed = true
    window.removeEventListener('resize', reset)
    themeObserver.disconnect()
    if (originalStyle === null) svg.removeAttribute('style')
    else svg.setAttribute('style', originalStyle)
    if (marker.parentNode) marker.replaceWith(svg)
    dialog.remove()
    document.body.style.overflow = previousOverflow
    if (activeClose === close) activeClose = undefined
    if (previousFocus?.isConnected) previousFocus.focus()
  }
  const button = (key: string, text: string, action: () => void) => {
    const el = document.createElement('button')
    el.type = 'button'
    el.textContent = text
    el.title = el.ariaLabel = translate(key)
    el.onclick = action
    toolbar.append(el)
    return el
  }
  button('chat.diagram_zoom_out', '−', () => zoom(1 / 1.5))
  button('chat.diagram_zoom_in', '+', () => zoom(1.5))
  button('chat.diagram_reset', translate('chat.diagram_reset'), reset)
  const closeButton = button('chat.image_close', '×', close)
  const hint = document.createElement('span')
  hint.textContent = translate('chat.diagram_hint')
  toolbar.append(hint)
  const points = new Map<number, { x: number; y: number }>()
  let gesture: { x: number; y: number; distance: number } | undefined
  let start: { x: number; y: number; blank: boolean } | undefined
  let moved = false
  let multi = false
  let lastTap = { time: 0, x: 0, y: 0 }
  const position = (e: PointerEvent) => {
    const r = stage.getBoundingClientRect()
    return { x: e.clientX - r.left, y: e.clientY - r.top }
  }
  const geometry = () => {
    const p = [...points.values()]
    return p.length > 1
      ? { x: (p[0].x + p[1].x) / 2, y: (p[0].y + p[1].y) / 2, distance: Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y) }
      : { ...p[0], distance: 0 }
  }
  stage.onpointerdown = (e) => {
    if (e.button !== 0) return
    e.preventDefault()
    const p = position(e)
    if (!points.size) {
      start = { ...p, blank: e.target === stage }
      moved = false
      multi = false
    } else multi = true
    points.set(e.pointerId, p)
    stage.setPointerCapture?.(e.pointerId)
    gesture = geometry()
  }
  stage.onpointermove = (e) => {
    if (!points.has(e.pointerId) || !gesture) return
    points.set(e.pointerId, position(e))
    const next = geometry()
    if (start && Math.hypot(next.x - start.x, next.y - start.y) > 6) moved = true
    if (points.size > 1 && gesture.distance > 0) {
      view = zoomAt(view, next.distance / gesture.distance, gesture.x, gesture.y)
    }
    view.x += next.x - gesture.x
    view.y += next.y - gesture.y
    gesture = next
    draw()
  }
  const end = (e: PointerEvent) => {
    if (!points.has(e.pointerId)) return
    points.delete(e.pointerId)
    if (points.size) { gesture = geometry(); return }
    gesture = undefined
    if (e.type !== 'pointerup' || moved || multi || !start) return
    if (start.blank) { close(); return }
    const now = Date.now()
    if (now - lastTap.time < 350 && Math.hypot(start.x - lastTap.x, start.y - lastTap.y) < 30) {
      if (view.scale > 1.1) reset()
      else zoom(3, start.x, start.y)
      lastTap.time = 0
    } else lastTap = { time: now, x: start.x, y: start.y }
  }
  stage.onclick = (e) => e.preventDefault()
  stage.onpointerup = end
  stage.onpointercancel = end
  stage.onlostpointercapture = end
  stage.addEventListener('wheel', (e) => {
    e.preventDefault()
    const r = stage.getBoundingClientRect()
    zoom(Math.exp(-Math.max(-100, Math.min(100, e.deltaY)) * 0.01), e.clientX - r.left, e.clientY - r.top)
  }, { passive: false })
  dialog.addEventListener('cancel', (e) => { e.preventDefault(); close() })
  dialog.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { e.preventDefault(); close() }
  })
  const themeObserver = new MutationObserver(close)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  window.addEventListener('resize', reset)
  dialog.showModal()
  reset()
  closeButton.focus()
  activeClose = close
  return close
}

/** Delegation also handles diagrams replaced during streamed markdown updates. */
export function setupMermaidViewer(root: HTMLElement): () => void {
  let close: (() => void) | undefined
  const activate = (e: Event) => {
    const diagram = (e.target as Element).closest?.('.mermaid-diagram')
    if (!diagram || !root.contains(diagram)) return
    const svg = diagram.querySelector('svg')
    if (!svg) return
    e.preventDefault()
    close = openMermaidViewer(svg)
  }
  const key = (e: KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') activate(e)
  }
  root.addEventListener('click', activate)
  root.addEventListener('keydown', key)
  return () => {
    root.removeEventListener('click', activate)
    root.removeEventListener('keydown', key)
    close?.()
  }
}
