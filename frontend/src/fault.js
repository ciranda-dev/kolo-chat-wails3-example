/** The data folder Start could not use, which the person moves aside to join again: Go sets dir in the cause, '' otherwise. */
export const folderToMove = (err) => (typeof err?.cause?.dir === 'string' ? err.cause.dir : '')

/** The layout of a data folder Start refused: its number, -1 when layout.json is damaged, 0 otherwise. */
export const folderLayout = (err) => (Number.isInteger(err?.cause?.layout) ? err.cause.layout : 0)

/** The key of the sentence that says what to do with a data folder Start refused, '' when there is no folder. */
export function startFaultKey(err) {
  if (err?.cause?.open === true) {
    return 'start.openFolder'
  }
  if (folderLayout(err) !== 0) {
    return 'start.otherLayout'
  }
  return folderToMove(err) ? 'start.moveFolder' : ''
}
