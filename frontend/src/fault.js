/** Whether Start failed because the data directory was made by an older version: Go sets rejoin in the cause. */
export const needsRejoin = (err) => err?.cause?.rejoin === true

/** The data folder Start could not use, which the person moves aside to join again: Go sets dir in the cause, '' otherwise. */
export const folderToMove = (err) => (typeof err?.cause?.dir === 'string' ? err.cause.dir : '')

/** The layout of a data folder Start refused: 1 for one of 0.1.9 or older, 2 for 0.1.10, another number for another version, 0 otherwise. */
export const folderLayout = (err) => (Number.isInteger(err?.cause?.layout) ? err.cause.layout : 0)

/** The key of the sentence that says what to do with a data folder Start refused, '' when there is no folder. */
export function startFaultKey(err) {
  if (err?.cause?.open === true) {
    return 'start.openFolder'
  }
  const layout = folderLayout(err)
  if (layout === 1) {
    return 'start.oldLayout'
  }
  if (layout === 2) {
    return 'start.layout2'
  }
  if (layout !== 0) {
    return 'start.otherLayout'
  }
  return folderToMove(err) ? 'start.moveFolder' : ''
}
