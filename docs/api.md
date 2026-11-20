# Go API and events

The window is a web page that calls Go services through Wails bindings and listens to events.

## Conversations

The service the window calls. It keeps one engine (`ChatService`) per conversation folder and routes every call by
the conversation's ID (see [how-it-works.md](how-it-works.md#several-conversations)).

- `Start` returns an `AppInfo`: `conversations`, `maxConversations`, `hasPins` (the example has pins; `Create` works either way), `network` (`mode` `public` or `own`; `nodes` lists the entry nodes of `own`, which are yours, and is empty for `public`, whose addresses never reach the window), `first` (the conversation that starts first, the one to show) and `version` (the release, set from the git tag at build time, `dev` for a build that is not a release). It returns after the first four engines have started; the others start in the background, a few at a time (see [how-it-works.md](how-it-works.md#staying-connected)). It fails only for a
  bad setting (`*ConfigError`) or a refused data folder (`*DataDirError`, see
  [configuration.md](configuration.md#the-data-folder)).
- `List`: each `ConversationInfo` has `id`, `name` (local, only you see it), `me` (your kolo-id in it), `joined`
  (false for the join in progress), `admin`, `state` (`connecting` while the room waits for its turn to start or reaches the network, `online`, `reconnecting` while it looks for the peers it just lost, `offline` while no entry node answers and it asks again by itself; empty when it did not start), and `error` and `folder` when it did not start.
- `Create(name)`, `Join(code, name)`, `CancelJoin`, `Rename(conv, name)`
- `Send(conv, room, text)`, `Members(conv)`, `Rooms(conv)`, `IsAdmin(conv)`: a call about a room that has not started yet waits for its engine; `Send` asks for it to start first and waits up to 30 seconds (`ErrStarting`). `Send` keeps the message in the room's log whatever the network does and returns no network error
- `Focus(conv)`: the room on screen, which starts before the others if it has not started yet
- `SetProfile(conv, name, avatar)`, `AddRoom(conv, name)` and `RemoveMember(conv, key)` (admins only, see
  [shared-state.md](shared-state.md)); a member has `key`, `name`, `avatar`, `admin`, `seat` (it settles changes;
  nobody can remove it), `pending` (its admission is not settled yet) and `flagged` (an entry could not be checked,
  or the member list refused its entries) and `mismatch` (a message it sent directly differs from the entry it signed)
- `CreateInvite(conv, options)` (`ttlSeconds`, `uses`), `Invites(conv)`, `Revoke(conv, id)`,
  `Approve(conv, request, admin)` (as a member or as an admin, for good), `Deny(conv, request)`; all but `Invites`
  need an admin
- `PlanLeave(conv)` (a `LeavePlan` with `token`, `name`, `dir`, `delete`, `keep`, `others`, `founder`, `admins`,
  `configured`), `Leave(conv, token, to)` (`to` is an admin's key for the founder's hand over, or `""`) and
  `CancelLeave(conv)` (see [how-it-works.md](how-it-works.md#leaving))

Events: `chat:conversations` carries the list of `ConversationInfo` as it is, and is sent again when a room's state changes. Every other event is a `ConvEvent`,
`{ "conv": <id>, "data": <payload> }`, with the payloads below. The example's own relay status has `conv` `""`.

| Event | When |
|---|---|
| `chat:msg` | a message, with its `room`, and its `time` (the sender's clock in Unix milliseconds, absent for a message that has none) |
| `chat:history` | once on open: the messages shown before and your own (`mine`), oldest first, each with `via` `history` and its `time` (see [how-it-works.md](how-it-works.md#history)) |
| `chat:peers` | the peers online changed |
| `chat:pin` | the state of the pins changed: `error` when none works, `warning` when some do not, `locked` (messages, from a pin or a member, that cannot be opened yet because storing their keys failed; 0 while keys are being derived), and `pins` with each pin's `addr`, `key`, `push` and `pull`; `pins` is empty, with no `error`, for a conversation with no pin |
| `chat:relays` | at start, and when the state of a connection relay changes: `seq`, which grows with every event of that conversation (keep the one with the highest, per conversation, since events can arrive out of order), `from` (`-relay`, `-relay-file`, both, `KOLO_CHAT_RELAYS`, or empty without relays) and `relays` with each relay's `addr`, `key`, `state` (`waiting`, `ready` or `failed`) and `error` (`wrong-key`, `bad-key`, `not-allowed`, `full` or `unreachable`) |
| `chat:invite-request` | a request to join, or one that is gone |
| `chat:invite` | an invite changed: someone joined, or it ended |
| `chat:join` | `joining`, `waiting`, `joined` or `failed`, with the sentence |
| `chat:profiles` | the members, when a name, an avatar or the member list changes |
| `chat:rooms` | the room list |
| `chat:state` | the member and room lists changed state: `pending`, `pendingRooms`, `paused` (members the braid is paused with), `settler`, `singleSettler`, and `halted`, `settlementHalted`, `diverged`, `unconfirmed`, `strandLost`, `noAnswer`, `otherGroup`, `notListed` (see [shared-state.md](shared-state.md#what-the-window-says)) |

Errors to match with `errors.Is`: `ErrUnknownConversation`, `ErrStarting`, `ErrTooManyConversations`, `ErrInvalidName`,
`ErrOldLayout`, `ErrNotAdmin`, `ErrFull`, `ErrSettler`, `ErrUnknownMember`, `ErrWaitingForMember`, `ErrCannotWrite`,
`ErrHandoverStopped`, `ErrInvalidProfile`, `ErrInvalidRoom`, `ErrRoomExists`, `ErrTooManyRooms`, `ErrUnknownRoom`,
`ErrNotInConversation` and `ErrLeaveNotConfirmed`. A failed join is an `*InviteError` (see
[how-it-works.md](how-it-works.md#invites)), and a bad setting a `*ConfigError` (see
[configuration.md](configuration.md)).

## NotchService

Methods the windows call: `State`, `Settings`, `SetEnabled`, `SetShowText`, `SetLanguage`, `Show`, `Hide`, `Hold`, `Release`, `OpenChat` and `ReportError(message, stack)`, which writes an error of either window to the terminal on a line that starts with `frontend error:` (cut to 8192 characters). Event: `notch:state`, sent to the notch window only, with `conversation`, the local name of the newest message's conversation.

Events are not made lists on the way, as method results are: every list in a payload is `[]`, never `null`, and the window reads each list through `list()` in `frontend/src/lists.js`.
