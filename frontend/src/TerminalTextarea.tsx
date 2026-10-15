import { useLayoutEffect, useRef } from 'react'
import type { RefObject, TextareaHTMLAttributes } from 'react'

// Keep the native textarea for editing, selection, accessibility, and IME.
// A hidden copy measures the caret using the browser's own text layout.
export function TerminalTextarea({ textareaRef, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement> & {
  textareaRef: RefObject<HTMLTextAreaElement | null>
}) {
  const mirrorRef = useRef<HTMLDivElement>(null)
  const caretRef = useRef<HTMLSpanElement>(null)
  const updateRef = useRef<() => void>(() => {})

  useLayoutEffect(() => {
    const input = textareaRef.current
    const mirror = mirrorRef.current
    const caret = caretRef.current
    if (!input || !mirror || !caret) return
    let composing = false
    let disposed = false
    const update = () => {
      if (disposed) return
      const visible = document.activeElement === input && input.selectionStart === input.selectionEnd && !composing
      caret.hidden = !visible
      input.classList.toggle('block-caret-active', visible)
      if (!visible) return
      const style = getComputedStyle(input)
      for (const property of ['font-family', 'font-size', 'font-weight', 'font-style', 'font-stretch', 'font-variant', 'font-kerning', 'font-feature-settings', 'font-variation-settings', 'text-rendering', 'line-height', 'letter-spacing', 'word-spacing', 'text-transform', 'text-indent', 'tab-size', 'padding-top', 'padding-right', 'padding-bottom', 'padding-left', 'direction']) {
        // The DOS theme sets font sizes with !important, including on spans.
        mirror.style.setProperty(property, style.getPropertyValue(property), 'important')
      }
      mirror.style.width = `${input.clientWidth}px`
      const marker = document.createElement('span')
      marker.style.setProperty('font', 'inherit', 'important')
      marker.textContent = '\u200b'
      mirror.replaceChildren(document.createTextNode(input.value.slice(0, input.selectionStart)), marker, document.createTextNode(input.value.slice(input.selectionStart)))
      const rect = marker.getBoundingClientRect()
      const origin = mirror.getBoundingClientRect()
      caret.style.left = `${rect.left - origin.left - input.scrollLeft}px`
      caret.style.top = `${rect.top - origin.top - input.scrollTop}px`
      caret.style.height = `${rect.height}px`
      caret.style.setProperty('font', style.font, 'important')
      caret.style.width = '1ch'
    }
    updateRef.current = update
    const startComposition = () => { composing = true; update() }
    const endComposition = () => { composing = false; update() }
    const events = ['input', 'select', 'keyup', 'click', 'scroll', 'focus', 'blur'] as const
    events.forEach(event => input.addEventListener(event, update))
    document.addEventListener('selectionchange', update)
    input.addEventListener('compositionstart', startComposition)
    input.addEventListener('compositionend', endComposition)
    const observer = new ResizeObserver(update)
    observer.observe(input)
    void document.fonts.ready.then(update)
    update()
    return () => {
      disposed = true
      observer.disconnect()
      events.forEach(event => input.removeEventListener(event, update))
      document.removeEventListener('selectionchange', update)
      input.removeEventListener('compositionstart', startComposition)
      input.removeEventListener('compositionend', endComposition)
    }
  }, [textareaRef])

  useLayoutEffect(() => { updateRef.current() }, [props.value])

  return <div className="terminal-input">
    <textarea {...props} ref={textareaRef} />
    <div ref={mirrorRef} className="caret-mirror" aria-hidden="true" />
    <span ref={caretRef} className="input-block-caret" aria-hidden="true" hidden />
  </div>
}
