/** The code to show for an invite: only while it is active, and only if this window created it. */
export const shownCode = (invite, codes) => (invite?.state === 'active' ? codes?.[invite.id] ?? '' : '')

/** Copies code with setText (the Wails clipboard), else with fallback (the browser's), and says whether one worked. */
export async function copyCode(code, setText, fallback) {
  if (!code) {
    return false
  }
  for (const write of [setText, fallback]) {
    try {
      if (write) {
        await write(code)
        return true
      }
    } catch {
      // try the next one
    }
  }
  return false
}
