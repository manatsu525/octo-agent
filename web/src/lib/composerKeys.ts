// Which submit the composer's Enter key means.
//
// Three-way, and the order matters: Cmd/Ctrl+Enter has to be recognised before
// plain Enter, or the send branch swallows it (both are `key === 'Enter'` with
// shift up). Shift+Enter is neither — it falls through to the textarea, which
// inserts the newline itself.

export interface SubmitKeyEvent {
  key: string
  metaKey?: boolean
  ctrlKey?: boolean
  shiftKey?: boolean
}

// 'queue' runs the message as its own turn after the one in flight; 'send'
// submits normally (mid-turn that means steering the running turn); null means
// this key is not a submit and the caller should keep handling it.
export function submitIntent(e: SubmitKeyEvent, touch: boolean = isTouchPrimary()): 'queue' | 'send' | null {
  if (e.key !== 'Enter') return null
  if (e.metaKey || e.ctrlKey) return 'queue'
  if (e.shiftKey) return null
  // On a phone/tablet soft keyboard there is no Shift+Enter, so plain Enter
  // inserts a newline and the send button is the only way to submit.
  if (touch) return null
  return 'send'
}

// True on touch-first devices (phones, tablets without a mouse): no hover and
// a coarse primary pointer. Desktop browsers keep Enter-to-send.
export function isTouchPrimary(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia('(hover: none) and (pointer: coarse)').matches
}
