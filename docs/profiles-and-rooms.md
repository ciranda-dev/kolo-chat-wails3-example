# Profiles, rooms and members

Every member has a profile (a display name and an avatar), and the group shares a list of rooms and members. Profiles
are in `profile.go`, with [kolo-kv](https://github.com/ciranda-dev/kolo-kv). The rooms and the members are shared state,
which every member must agree on, so they are in a [kolo-braid](https://github.com/ciranda-dev/kolo-braid) braid: see
[shared-state.md](shared-state.md).

## Profiles: one writer per key

kolo-kv is a key-value store with a single writer, kept in a kolo-log log. So every member gets a second log in its
store, the directory, that only that member writes, with a kolo-kv store on it. It holds one key, `profile`, and a
member's directory is read for that key and for that member only.

- **Writing.** A log has one signing key, so a member cannot write another member's log. A member's copy of another
  directory is a read-only kolo-kv reader (`kolokv.OpenReader`), whose `Put` returns `kolokv.ErrReadOnly`.
- **Reading.** Whatever a member writes in its own directory is its own profile, and only its own.

Each conversation has its own profiles: you pick your name and avatar in each one.

## Rooms and members

- The member list is the braid's roster. Admins are chosen when a request is approved; the person who started the
  conversation is an admin and settles changes. `Members` lists the settler first, then the admins, then the others,
  each with `admin`, `seat`, `pending` (its admission is not settled yet) `flagged` and `mismatch` (a message it sent directly differs from the entry it signed).
- The room list is `general` (every conversation has it, and nothing is written for it) plus the rooms admins added,
  from the braid's view. `AddRoom` from a member who is not an admin returns `ErrNotAdmin`; a name that exists, ASCII
  letters in any case, is `ErrRoomExists`. Two admins who add one name at the same moment end with one room.
- Rooms are never removed or renamed, and a room has no members of its own: everybody in the conversation sees every
  room. A conversation has at most 64 rooms and 64 members.

## Sync

- The hello carries the signed header of the directory log next to the header of the messages log. A hello with a
  directory header that is not the sender's own, or that is the messages log, is refused.
- The example opens a read-only copy of every member's directory (`directory` in `profile.go`) with a kolo-kv reader on it.
  The copies are in the store, so a restart shows the names it kept before anyone connects.
- On a hello that answers ours, and when a peer says its directory changed (a `dir` message after `SetProfile`), the
  example pulls that directory from the peer with `kolosync.PullCiphertext`, derives the keys of the new entries (see
  `entrykeys.go`), and calls `Refresh` on the reader. Only members on the member list are pulled.
- At start the example pulls the directory of every member on the list from the pins, as it does for the messages.
- Both of our logs go to every pin: the example's capability for each pin lists the two log IDs (`pin.Capability.Logs`).
- Each profile is sealed with the conversation key, bound to the author and to its key, as the messages are. The pin
  learns nothing from it.

The two logs of a member look alike, so the `directory-log` file tells them apart. If that file is lost and the store
holds two logs of the member, `Start` fails instead of guessing.

## Names that changed while you were offline

The directory is a log like any other: the pin has its ciphertext, and the example derives its entry keys from the
conversation key. A name that changed while you were offline shows once the example pulled the directory from a pin, with
the author offline. A new room or member shows once your braid syncs with a member online. kolo-kv replays the entries
up to the first one without a key, so an entry whose key does not match its record stops that member's directory there,
and the member list marks the member "unchecked".

## Rooms and messages

A message holds its room inside the sealed bytes (`{"v":1,"room":"dev","text":"...","t":1795000000000}`, `t` being the
sender's clock), so the pin cannot tell the rooms apart. `Send(room, text)` refuses a room that is not in the list
(`ErrUnknownRoom`). The window shows the messages of `general`, the default room of the selected conversation, and keeps the others in the
store. An entry from step 4, which is plain text, shows in `general`.

## Names are not identities

Anyone can pick any name, also the name of another member. When two members share a name, the window adds the start
of the key, and the key is in the tooltip. Names and avatars are checked when they are written (`SetProfile`,
`ErrInvalidProfile`: a name of 1 to 40 characters, an avatar of up to 16 characters with no `/ : \ < >` and no control
characters) and again when they are read, since a member can write what it wants in its own log. The avatar is a
reference, such as an emoji, and the window shows it as text: there is no image upload.

A new name is a new `Put`, and the old entry stays in the directory with its key, readable by every member that
already has them. The example does not delete profile entries, so a member cannot take back a name.

## Try it

With the demo of the README running, click Profile in each window and save a name and an avatar: the other window
shows the name in the member list and on the messages. In each window click Members: Alice, an admin, sees Remove next
to Bob; Bob, approved as a member, sees no Remove button. The window shows the default room of each conversation,
`general`, and no list of rooms inside it; the engine, `Rooms`, `AddRoom` and `Send(conv, room, text)` keep the rooms of
a conversation, and a message for another room is kept and not shown.
