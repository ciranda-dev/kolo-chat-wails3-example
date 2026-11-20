/** A message that comes more than this long after the one before it starts a new group: 5 minutes. */
export const GROUP_GAP = 5 * 60 * 1000

const dayOf = (time) => new Date(time).toDateString()

/**
 * Turns messages, oldest first, into what the window draws: a day chip when the day changes (and before the first
 * message), then groups of consecutive messages of one author. A group ends when the author changes, when more than
 * gap passed since the message before, or at a day chip. A message with no time (0) never breaks a group by time and
 * never makes a chip.
 * @param {{ id: number, from: string, mine: boolean, time: number, via: string }[]} msgs
 * @returns {({ kind: 'day', key: string, time: number } | { kind: 'group', key: string, from: string, mine: boolean, time: number, vias: string[], msgs: any[] })[]}
 */
export function groupMessages(msgs, gap = GROUP_GAP) {
  const items = []
  let group = null
  let prev = null
  let day = ''
  for (const m of msgs) {
    let fresh = !group || group.from !== m.from || group.mine !== m.mine
    if (m.time > 0) {
      if (dayOf(m.time) !== day) {
        day = dayOf(m.time)
        items.push({ kind: 'day', key: `day-${m.id}`, time: m.time })
        fresh = true
      }
      if (prev && prev.time > 0 && Math.abs(m.time - prev.time) > gap) {
        fresh = true
      }
    }
    if (fresh) {
      group = { kind: 'group', key: `group-${m.id}`, from: m.from, mine: m.mine, time: 0, vias: [], msgs: [] }
      items.push(group)
    }
    group.msgs.push(m)
    if (m.time > 0) {
      group.time = m.time
    }
    if ((m.via === 'pin' || m.via === 'peer') && !group.vias.includes(m.via)) {
      group.vias.push(m.via)
    }
    prev = m
  }
  return items
}
