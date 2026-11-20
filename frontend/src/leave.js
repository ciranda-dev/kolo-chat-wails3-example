/**
 * The steps of Leave conversation: 'idle', then 'confirm' with the plan from PlanLeave, then 'leaving' once the
 * user confirms, and 'failed' with the error. Only a confirm with a plan gets to 'leaving'.
 * @param {{ step: string, plan?: any, error?: string }} state
 * @param {{ type: string, plan?: any, error?: string }} action
 */
export function leaveStep(state, action) {
  switch (action.type) {
    case 'plan':
      return action.plan?.token ? { step: 'confirm', plan: action.plan } : { step: 'failed', error: 'No plan to confirm.' }
    case 'confirm':
      return state.step === 'confirm' && state.plan?.token ? { step: 'leaving', plan: state.plan, to: action.to ?? '' } : state
    case 'error':
      return { step: 'failed', error: action.error }
    case 'cancel':
    case 'left':
      return { step: 'idle' }
  }
  return state
}

/** The token to call Leave with: only while leaving, after a confirm. */
export const leaveToken = (state) => (state.step === 'leaving' ? state.plan?.token ?? '' : '')

/** The admin a founder's Leave hands settlement over to, '' for none. */
export const leaveTarget = (state) => (state.step === 'leaving' ? state.to ?? '' : '')

/** The admins a founder can hand settlement over to: those in the plan, none for anyone else. */
export const handoverAdmins = (plan) => (plan?.founder && Array.isArray(plan.admins) ? plan.admins : [])

/** The keys of the sentences the confirm step shows, in order: the warning always, then what applies. */
export function leaveNotes(plan) {
  const notes = ['settings.leave.warning']
  if (plan?.founder) {
    notes.push(handoverAdmins(plan).length > 0 ? 'state.handoverNote' : 'state.founderLeave')
  }
  if (plan?.configured) {
    notes.push('settings.leave.configured')
  }
  return [...notes, 'settings.leave.then']
}
