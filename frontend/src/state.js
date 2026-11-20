import { list } from './lists.js'

/** The room every conversation uses in the window: the one each conversation starts with. Other rooms stay in the data and the API. */
export const DEFAULT_ROOM = 'general'

/** The payload of "chat:state" with every field in place. */
export const stateStatus = (data) => ({
  singleSettler: data?.singleSettler === true,
  settler: data?.settler === true,
  pending: Number(data?.pending) || 0,
  pendingRooms: list(data?.pendingRooms),
  paused: Number(data?.paused) || 0,
  halted: data?.halted === true,
  settlementHalted: data?.settlementHalted === true,
  diverged: data?.diverged === true,
  unconfirmed: data?.unconfirmed === true,
  strandLost: data?.strandLost === true,
  noAnswer: data?.noAnswer === true,
  otherGroup: data?.otherGroup === true,
  notListed: data?.notListed === true,
})

/** Whether me is an admin of the member list: who invites, approves, removes and adds rooms. */
export const isAdmin = (members, me) => list(members).some((m) => m.key === me && m.admin)

/** Whether the window offers Remove on m: to admins only, never on the settler, whom nobody can remove, nor on yourself. */
export const canRemove = (members, me, m) => isAdmin(members, me) && !m.seat && m.key !== me

/** The marks next to a member, as i18n keys. */
export function memberMarks(m) {
  const marks = []
  if (m.admin) {
    marks.push('state.admin')
  }
  if (m.pending) {
    marks.push('state.pending')
  }
  if (m.flagged) {
    marks.push('state.flagged')
  }
  if (m.mismatch) {
    marks.push('state.mismatch')
  }
  return marks
}

/** The sentences "chat:state" asks for, most important first, each { key, n }. */
export function stateBanners(s) {
  const out = []
  const add = (on, key, n) => on && out.push({ key, n })
  add(s.diverged, 'state.diverged')
  add(s.halted, 'state.halted')
  add(s.settlementHalted, 'state.settlementHalted')
  add(s.strandLost, 'state.strandLost')
  add(s.otherGroup, 'state.otherGroup')
  add(s.notListed, 'state.notListed')
  add(s.noAnswer, 'state.noAnswer')
  add(s.paused > 0, 'state.paused', s.paused)
  add(s.unconfirmed && !s.strandLost, 'state.unconfirmed')
  return out
}
