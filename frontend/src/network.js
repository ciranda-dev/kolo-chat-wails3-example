/** The payload of AppInfo.network with every field in place: mode is "own" or else "public". */
export const networkInfo = (data) => ({
  mode: data?.mode === 'own' ? 'own' : 'public',
  nodes: Array.isArray(data?.nodes) ? data.nodes.map(String) : [],
})

/** The key of the sentence that says which network the app joins. */
export const networkKey = (info) => (info.mode === 'own' ? 'settings.network.own' : 'settings.network.public')

/** The states of a room's connection: it waits to start or reaches the network, is online, looks for the peers it just lost, or cannot reach the network. */
export const ROOM_STATES = ['connecting', 'online', 'reconnecting', 'offline']

/** The state of a room's connection, '' for a room that did not start. */
export const roomState = (row) => (ROOM_STATES.includes(row?.state) ? row.state : '')

/** The key of the short sentence for a state that is not "online", '' for the others. */
export const stateKey = (state) => ({ connecting: 'net.connecting', reconnecting: 'net.reconnecting', offline: 'net.offline' })[state] ?? ''

/** The sentences under the messages while the room is not online: why, for an unreachable network, and that messages are kept. */
export function netNotes(state, network) {
  const keys = []
  if (state === 'offline') {
    keys.push(`network.unreachable.${network?.mode === 'own' ? 'own' : 'public'}`, 'network.options')
  }
  if (state === 'offline' || state === 'reconnecting') {
    keys.push('net.kept')
  }
  return keys
}

/** What the header says under the room's name: the state while the room is not online and nobody is, else how many are online. */
export const headerState = (state, peers) => (peers > 0 ? '' : stateKey(state))
