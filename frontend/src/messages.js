/** @typedef {{ id: number, from: string, room: string, text: string, mine: boolean, via: string, time: number }} Msg */

/**
 * toMsg turns the payload of "chat:msg", or one item of "chat:history", into a Msg with the given id. Its time is the
 * sender's, in Unix milliseconds; a message that has none gets now (0 for the history: its time is unknown).
 */
export function toMsg(data, id, now = 0) {
  const item = typeof data === 'object' && data ? data : { text: String(data ?? '') }
  return {
    id,
    from: item.from ?? '',
    room: item.room ?? 'general',
    text: item.text ?? '',
    mine: item.mine === true,
    via: item.via ?? '',
    time: Number(item.time) > 0 ? Number(item.time) : now,
  }
}

/** withHistory puts the history above the live messages, in place of any history shown before. */
export function withHistory(prev, history) {
  return [...history, ...prev.filter((m) => m.via !== 'history')]
}
