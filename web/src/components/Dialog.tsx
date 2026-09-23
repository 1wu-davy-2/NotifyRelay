import { useEffect, useRef, type ReactNode } from 'react'

/**
 * A modal, on the native <dialog> element.
 *
 * Native rather than a div with a fixed position, and the difference is not
 * cosmetic: showModal() gives focus trapping, the top layer (so nothing can
 * paint over it), Escape to close, and inertness of the page behind it — all of
 * which a hand-rolled modal has to reimplement, and usually reimplements
 * partially.
 *
 * ---------------------------------------------------------------------------
 * `dismissible` is false for exactly one dialog in this interface, and it is
 * the one that matters.
 *
 * The API key's token exists in the create response and nowhere else — the
 * store keeps a digest. A stray click on the backdrop or a reflexive Escape
 * would discard the only view of a value the operator cannot retrieve, and they
 * would find out when the integration they just wired up returns 401. So that
 * dialog closes only by its own button, which is why this prop exists rather
 * than the behaviour being uniform.
 * ---------------------------------------------------------------------------
 */
export function Dialog({
  open,
  onClose,
  title,
  children,
  dismissible = true,
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  children: ReactNode
  /** False makes the dialog close only through its own buttons. */
  dismissible?: boolean
}) {
  const ref = useRef<HTMLDialogElement>(null)

  /**
   * Whether the last close was asked for by this component.
   *
   * A ref rather than state because it has to be read inside a DOM event
   * handler that fires before React has re-rendered — see the close backstop
   * below.
   */
  const asked = useRef(false)

  useEffect(() => {
    const el = ref.current
    if (!el) return

    // showModal() on an already-open dialog throws, and so does close() on one
    // that is not open. The `open` property is the DOM's own answer to both.
    if (open && !el.open) {
      asked.current = false
      el.showModal()
    }
    if (!open && el.open) {
      asked.current = true
      el.close()
    }
  }, [open])

  useEffect(() => {
    const el = ref.current
    if (!el) return

    // Escape fires a cancelable `cancel` event, and the specification says
    // preventing it keeps the dialog open. Chrome does not: as of 152 it fires
    // the event, honours the preventDefault as far as `defaultPrevented` is
    // concerned, and closes the dialog anyway. Verified against a bare <dialog>
    // with no React in it, so it is the browser and not this component.
    //
    // The backstop below is therefore the mechanism, and this handler is only
    // here for the browsers that do behave. Closing and reopening in the same
    // task is not visible: the dialog never leaves the top layer for a frame.
    //
    // Arrow constants rather than function declarations, so TypeScript keeps
    // the non-null narrowing of `el` inside them. A hoisted declaration could
    // in principle run before the guard above it.
    const onCancel = (event: Event) => {
      if (!dismissible) event.preventDefault()
    }

    const onCloseEvent = () => {
      if (!dismissible && !asked.current) {
        el.showModal()
      }
    }

    // A click on the backdrop targets the dialog element itself, because the
    // backdrop is a pseudo-element of it. The panel inside stops the event, so
    // anything that reaches here came from outside the panel.
    const onClick = (event: MouseEvent) => {
      if (dismissible && event.target === el) onClose()
    }

    el.addEventListener('cancel', onCancel)
    el.addEventListener('close', onCloseEvent)
    el.addEventListener('click', onClick)
    return () => {
      el.removeEventListener('cancel', onCancel)
      el.removeEventListener('close', onCloseEvent)
      el.removeEventListener('click', onClick)
    }
  }, [dismissible, onClose])

  return (
    <dialog ref={ref} className="dialog" aria-label={typeof title === 'string' ? title : undefined}>
      <div className="dialog-panel">
        <h3>{title}</h3>
        {children}
      </div>
    </dialog>
  )
}
