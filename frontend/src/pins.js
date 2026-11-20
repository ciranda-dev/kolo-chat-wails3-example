/** The first 8 characters of a pin's key, enough to tell pins apart. */
export const shortKey = (key) => String(key ?? '').slice(0, 8)

/** What is wrong with a pin of the "chat:pin" event: its push sentence, else its pull sentence, else ''. */
export const pinProblem = (p) => String(p?.push || p?.pull || '')

/** The note about messages that cannot be opened yet: storing their keys failed, and the next sync tries again. */
export const pinnedNotes = (locked) =>
  locked > 0 ? [`${locked} ${locked === 1 ? 'message' : 'messages'} could not be opened yet. The next sync tries again.`] : []
