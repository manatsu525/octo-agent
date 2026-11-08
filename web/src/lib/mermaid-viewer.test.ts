import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { openMermaidViewer, setupMermaidViewer, zoomAt } from './mermaid-viewer'
import { locale } from './i18n'

beforeEach(() => {
  HTMLDialogElement.prototype.showModal = vi.fn(function(this: HTMLDialogElement) { this.open = true })
})
afterEach(() => { document.querySelector('dialog')?.dispatchEvent(new Event('cancel')); document.body.replaceChildren(); locale.set('en') })
function fixture() {
  const root = document.createElement('div')
  root.innerHTML = '<div class="mermaid-diagram" tabindex="0"><svg id="test-diagram" viewBox="0 0 1000 500" style="max-width:100%"><style>#test-diagram text { fill: red; }</style><text>Readable</text></svg></div>'
  document.body.append(root)
  return { root, svg: root.querySelector('svg')! }
}
describe('diagram viewer', () => {
  it('zooms around the pointer and clamps magnification', () => {
    expect(zoomAt({ x: 10, y: 20, scale: 1 }, 2, 100, 100)).toEqual({ x: -80, y: -60, scale: 2 })
    expect(zoomAt({ x: 0, y: 0, scale: 1 }, 100, 0, 0).scale).toBe(16)
    expect(zoomAt({ x: 0, y: 0, scale: 1 }, 0.001, 0, 0).scale).toBe(0.2)
  })
  it('moves the sanitized node without duplicate ids and restores its style', () => {
    const { svg, root } = fixture()
    const close = openMermaidViewer(svg)
    expect(document.querySelectorAll('#test-diagram')).toHaveLength(1)
    expect(document.querySelector('dialog svg')).toBe(svg)
    expect(svg.textContent).toContain('Readable')
    expect(svg.querySelector('style')?.textContent).toContain('#test-diagram')
    close(); close()
    expect(root.querySelector('svg')).toBe(svg)
    expect(svg.getAttribute('style')).toBe('max-width:100%')
    expect(document.querySelector('dialog')).toBeNull()
  })
  it('delegates keyboard activation and cleans up', () => {
    const { root } = fixture()
    const destroy = setupMermaidViewer(root)
    root.firstElementChild!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    expect(document.querySelector('dialog')).not.toBeNull()
    destroy()
    expect(root.querySelector('svg')).not.toBeNull()
    expect(document.querySelector('dialog')).toBeNull()
  })
  it('supports controls, wheel, Escape and Chinese labels', () => {
    locale.set('zh')
    const { svg } = fixture()
    openMermaidViewer(svg)
    expect(document.querySelector('dialog')?.getAttribute('aria-label')).toBe('图表预览')
    const canvas = document.querySelector<HTMLElement>('.mermaid-viewer-canvas')!
    const before = canvas.style.transform
    document.querySelector<HTMLButtonElement>('button[aria-label="放大"]')!.click()
    expect(canvas.style.transform).not.toBe(before)
    document.querySelector<HTMLButtonElement>('button[aria-label="适应屏幕"]')!.click()
    expect(canvas.style.transform).toBe(before)
    const wheel = new WheelEvent('wheel', { deltaY: -50, cancelable: true })
    document.querySelector('.mermaid-viewer-stage')!.dispatchEvent(wheel)
    expect(wheel.defaultPrevented).toBe(true)
    expect(canvas.style.transform).not.toBe(before)
    document.querySelector('dialog')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(document.querySelector('dialog')).toBeNull()
  })
  it('closes on theme changes and tolerates streamed source replacement', async () => {
    const { root, svg } = fixture()
    openMermaidViewer(svg)
    root.replaceChildren()
    document.documentElement.setAttribute('data-theme', 'dark')
    await Promise.resolve()
    expect(document.querySelector('dialog')).toBeNull()
    document.documentElement.removeAttribute('data-theme')
  })
  it('distinguishes backdrop taps, drags, double taps and pinch', () => {
    const { svg } = fixture()
    openMermaidViewer(svg)
    const stage = document.querySelector<HTMLElement>('.mermaid-viewer-stage')!
    const canvas = document.querySelector<HTMLElement>('.mermaid-viewer-canvas')!
    const pointer = (target: Element, type: string, id: number, x: number, y: number) => {
      const e = new Event(type, { bubbles: true, cancelable: true })
      Object.assign(e, { pointerId: id, clientX: x, clientY: y, button: 0 })
      target.dispatchEvent(e)
    }
    pointer(stage, 'pointerdown', 1, 50, 50)
    pointer(stage, 'pointermove', 1, 100, 100)
    pointer(stage, 'pointerup', 1, 100, 100)
    expect(document.querySelector('dialog')).not.toBeNull()
    const before = canvas.style.transform
    pointer(svg, 'pointerdown', 1, 100, 100)
    pointer(svg, 'pointerdown', 2, 200, 100)
    pointer(stage, 'pointermove', 2, 300, 100)
    pointer(stage, 'pointerup', 2, 300, 100)
    pointer(stage, 'pointerup', 1, 100, 100)
    expect(canvas.style.transform).not.toBe(before)
    for (let i = 0; i < 2; i++) {
      pointer(svg, 'pointerdown', 1, 100, 100)
      pointer(svg, 'pointerup', 1, 100, 100)
    }
    expect(document.querySelector('dialog')).not.toBeNull()
    pointer(stage, 'pointerdown', 1, 10, 10)
    pointer(stage, 'pointerup', 1, 10, 10)
    expect(document.querySelector('dialog')).toBeNull()
  })
})
