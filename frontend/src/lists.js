/** value when it is a list, else fallback: Go sends a nil slice as null, and an event reaches the window as Go sent it. */
export const list = (value, fallback = []) => (Array.isArray(value) ? value : fallback)

/** The payload of "chat:pin" with every field in place. */
export const pinStatus = (data) => ({
  error: data?.error ?? '',
  warning: data?.warning ?? '',
  locked: data?.locked ?? 0,
  pins: list(data?.pins),
})
