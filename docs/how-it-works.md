# How it works

Words used here:

- **Peer**: one running copy of the example.
- **Log**: a list of signed entries that only its writer can add to ([kolo-log](https://github.com/ciranda-dev/kolo-log)). Every member writes its own.
- **Pin**: an optional server that keeps the encrypted logs for members who are offline ([kolo-pin](https://github.com/ciranda-dev/kolo-pin)). It cannot read them. A conversation can use none, one or several.
- **Room**: what the window calls a conversation. The code, the API and these pages about the engine say conversation; the pages about the window say room.
- **Founder**: the person who starts the conversation: with Create a room in the window, or with a conversation key. The founder is an admin and settles changes to the member and room lists.
- **Admin**: a member approved as an admin, or the founder. Admins invite, approve, remove members and add rooms.
- **Braid**: each conversation's [kolo-braid](https://github.com/ciranda-dev/kolo-braid), which holds the member list and the room list that every member agrees on (see [shared-state.md](shared-state.md)).
- **Engine**: the part of the example that runs one conversation (`ChatService`); the example runs one per conversation.

## Sending a message

1. You type a message. The example seals it with the conversation key (XChaCha20-Poly1305) and appends it to your own log.
2. The example sends it straight to every connected peer, each in its own goroutine, so a peer whose connection died without the example knowing never holds the window. A failed direct send is only logged: the message is in your log, and a peer that was not reached pulls it when it connects.
3. The example pushes your log to every pin at once (with no pin, nothing is pushed), so members who are offline get it later. Each member issues its own pin capability (a signed permission) for its own log, one per pin.

Messages are ordered within one sender, by the sender's log. While the example runs, messages of different senders show in the order they arrive. No message shows twice.

Each message also carries, inside the sealed bytes, the time on the sender's clock (`t`, Unix milliseconds). Nothing checks that clock: it only orders the history (below), and a sender with a wrong clock moves its own messages there, never anyone else's.

## Opening the example after being offline

1. The example pulls the ciphertext of every member's log from every pin with [kolo-sync](https://github.com/ciranda-dev/kolo-sync) (`PullCiphertext`). Every head is checked against the member's key, so a pin that is behind changes nothing and the copy ends with the longest history any pin has.
2. Each entry's key is derived from the conversation key, so the example reads what it pulls from a pin or a member at once, with or without the sender online. kolo-log derives the key (`kololog.WithDerivedKeys`) from the entry secret, the log and the entry's index, and the example derives the entry secret from the conversation key with HKDF-SHA256 under its own label, `kolochat/entry-secret/v1`. No entry key is ever sent: members pull each other's logs as ciphertext too, exactly as from a pin. The code is in `entrykeys.go`.
3. A message whose key does not match is not shown, and the window marks its sender ("unchecked" in the member list): the sender's signed record commits to a key that is not the derived one, which only a broken or changed example writes. The example skips that message and still shows the sender's later ones; a sender with more than 256 such entries in its two logs (messages and profile changes together) is no longer pulled or read at all, even with a new log. If storing the keys fails, on a full or failing disk, nothing is marked: the window says how many messages could not be opened yet, and the next pull derives them again. Pulls happen at start, on each connection and after each direct message that comes after a gap.

Online means connected in this conversation. Two examples meet on the conversation's topic, a hash of its key, so a founder that starts with another key is in another conversation: its members never connect to it, and its new messages, pinned with that key, can never be read by them. The conversation's folder keeps the key from the first start (see [Starting and joining](#starting-and-joining)).

To see it with the demo of the README: close Bob's window, send two messages from Alice, close Alice's window too, and open Bob again. After a moment the two messages appear with "synced from pin", with Alice offline.

The pull from the pins happens when the example opens, from all of them at once, each with its own timeout. The sync from peers waits until every pin has answered or timed out. A pin that is unreachable is tried again, for the pull at open and for the push, with a growing, jittered wait from 5 seconds to a minute (and at once for the push on the next message), and once more when the network comes back.

### Without a pin

A conversation with no pin works the same way, with nothing to push to or pull from. Whenever two members are connected, each pulls the other's own log from the other (the same ciphertext pull as from a pin), so:

- A message reaches a member who is offline when its sender and that member are online together: the member pulls the sender's log then, and the message shows as coming from a "peer".
- A member who joins later pulls the log of each member who is online, so it gets the earlier messages of those members. The earlier messages of a member who is offline come when that member is online again. A member never serves another member's log, only its own.
- A message written while nobody else is online is kept in your own log and goes out the next time someone connects. If you lose your data folder before that, it is lost: no other copy exists.

The window says so, in English, Spanish and Portuguese, with one quiet line under the list of rooms when the selected room has no pin (or the example has none and no room is selected); the whole sentence is in its tooltip.

## History

On open, before anything new, the window gets the conversation back in one `chat:history` event: your own messages and the messages of others it showed before, read from the store in its folder, every room. It shows them above the live messages. Messages it never showed come after, the usual way, so nothing shows twice. The notch and the tray ignore `chat:history`, so the history never counts as unread.

A message that arrives directly is not in the store yet: the example pulls the sender's log right after it, which keeps the entry. A message shown in the moment before you closed the example, before that pull, is missing from the history; it shows again, once, as a new message, when the sender's log comes from a pin or the sender.

The order is the same after every restart:

- one sender's messages keep the order of its log;
- senders merge by the time on the sender's clock; a time earlier than the sender's previous message, or none, counts as that previous message's time;
- equal times go by the sender's key, then by log index.

The history holds the newest 256 messages (`maxHistory`), so its text is at most 16 MiB: the bytes one pin keeps for one member by default (`defaultPinQuota` / `maxMessage`, 16 MiB / 64 KiB). Older messages stay in the store. Messages cannot be deleted.

## Several conversations

One example keeps several conversations, each in a folder of its own under `<data>/conversations/` (see
[configuration.md](configuration.md#the-data-folder)). `Conversations` (`conversations.go`) lists the folders, starts
one engine per folder, a few at a time, and routes every call of the window by the conversation's ID; every event of an
engine reaches the window with that ID (`ConvEvent`).

Starting many conversations is paced: the window gets the list at once, and the engines start four at a time, the room on screen first (the window tells the example with `Focus`) and then the others, the one with the newest message first. See [Staying connected](#staying-connected).

Each engine has its own kolo-id, swarm, logs, pins and invites, as one example had before. Two rules of the libraries make
that the simple way: a pin keeps the newest capability of each owner, so two conversations under one kolo-id would
take each other's logs off a pin; and a connection to a peer key serves one protocol, so someone already connected to
you in one conversation could not run another conversation's invite on that connection. With one kolo-id per
conversation, the members of two conversations cannot tell from the kolo-id that one person is in both.

The derived entry keys, the quarantine of a member and its budget of 256 refused entries, the pins, `-pin-quota` and
the invites are all per conversation, as before. The connection relays are the example's: their key is checked once when
the example starts, and a wrong one shows in every conversation.

- **Create a room** (the + beside Rooms) asks for a name, which only you see, and starts a conversation that you own, with a key from
  `crypto/rand` and the pins of `-pin` or `-pin-file`.
- **Join a room** (the same menu) takes an invite code and a name, and makes a new folder with a new kolo-id: one invite is one
  conversation. Joining a conversation you are already in, with a new invite, makes a second one; the others see a
  new member. A join that fails or is cancelled deletes its folder, kolo-id included, and the next one starts with a new
  kolo-id (see [shared-state.md](shared-state.md#admissions-joining-again-and-the-bound)).
- The list shows the unread messages of each room (those of its default room, the one the window shows); the notch and the tray count them all.
- A local list of conversations is enough because one kolo-id lives on one device: using one account on several devices is a different problem and is not solved here.

## Staying connected

A room never ends because the network went away, and it never waits for the network before it works. The pieces are in `netstate.go` and `conversations.go`:

- **The first connection.** Each engine bootstraps from the entry nodes when it starts. If none answers (no internet, a busy entry node), the engine still starts: it opens its logs, so what you type is kept, and a loop asks again with a wait that doubles from 2 seconds up to 30, cut to a random value between its half and its whole so that rooms that failed together do not ask together. It never gives up. The conversation's topic is joined, and the invites are announced, as soon as an entry node answers.
- **A gentle start.** The public entry nodes let one IPv4 address send 50 requests a second, and a room puts about ten on them when it starts. The example starts the engines four at a time (`startSlots`), each place of the gate waiting 1.5 seconds plus up to 1 second at random (`startPause`, `startJitter`) after its engine has joined its topic, and the retries of a room that found the network down use the same gate. `Start` returns after the first four engines, so the list shows at once; a row whose engine has not started shows a dot and the state "connecting", a call about it waits for its engine, and a `Send` to it asks for it to start first (`startWait`, 30 seconds). The order is the conversation-key room, the room on screen, then the newest message (the time is kept in the room's `local.json`, at most once a minute), then the order the rooms were added.
- **Lost connections.** When the last connection to a peer ends, the room says "reconnecting" for 45 seconds (`lostGrace`) or until a peer is back, and asks the entry nodes once to learn whether the network is what went away: if they do not answer it says "could not reach the network, trying again" and asks with the same growing wait. kolo-swarm dials the peers again by itself, waiting at most a minute between two tries (`dialRetryMax`, its default is 10 minutes), and the peers' hellos make each side pull what the other wrote meanwhile. A connection a laptop's sleep or a Wi-Fi change killed is noticed by the 30-second idle timeout of kolo-transport (its keep-alive is 5 seconds).
- **What you type meanwhile.** `Send` writes the message to your log, which needs no network, and hands it to the connected peers; it never waits for one and never returns a connection error. The window keeps the text in the box only when `Send` fails, which it does for a message that is too long or a room that did not start, not for the network. The peers pull the message when the connection returns, and show each message once, in the order of its sender's log. A message you send is never held back by a state: only the pin and the direct delivery depend on the network.
- **The states** (`chat:network`, shown in the row and under the room's name): `connecting` while a room waits for its turn or reaches the network the first time, `online`, `reconnecting` and `offline`. They are plain words, never an error, and `offline` adds the three ways to choose a network.

## Starting and joining

- A founder starts a conversation with Create a room, or from the command line with `KOLO_CHAT_CONVERSATION_KEY` or `-conversation-key-file`, and optionally `-pin` or `-pin-file`; the example creates the conversation's braid.
- A joiner pastes an invite: the key, the pins and the braid's welcome come from it, once an admin approved the request.
- Both are kept in `conversation.json` in the conversation's folder (mode 0600), so the next start needs no flags. A folder that lost its `conversation.json` is refused at start, its row names the folder, and nothing in it is deleted, unless it is provably a join that did not finish (see [the data folder](configuration.md#the-data-folder) for the exact rule).
- A conversation folder whose logs use random entry keys, which only their writer holds, is refused: a log cannot change how it makes its keys. A data folder in a layout the example does not know is refused as a whole (see [configuration.md](configuration.md#the-data-folder)). The window names the folder and says, in your language, what to do: quit, move or rename that folder (nothing in it is deleted), open the example again, and start a new room or paste a new invite. Generate a `-conversation-key` once: running `export KOLO_CHAT_CONVERSATION_KEY=$(openssl rand -hex 32)` again gives a new one, and a new conversation.

## Leaving

Settings has Leave room, for the selected room. It first shows the plan (`PlanLeave`): the folder it deletes and what it keeps (`layout.json`, `notch.json` and your other conversations). Only Leave and delete, with the plan's one-time token, which works for that conversation only, calls `Leave`, which:

1. leaves the conversation's topic and stops its engine: the chat, the sync, the pushes to the pins and the invites;
2. deletes its folder: `conversation.json`, `members.json`, `directory-log`, `store/` (your logs and the copies of the others' logs), `invites/` (whose answers carry the conversation key), `pin-tokens/` (the capabilities you issued to the pins) and `identity.key`, your kolo-id in that conversation, which nothing else used;
3. leaves your other conversations as they are, and the window selects the next one, or shows the join screen.

The others are not told. They still list you as a member until an admin removes you, and keep what you sent; the pins keep the encrypted copies until your capability expires, and every member, past ones included, can still read them: their keys are derived from the conversation key. A new invite brings you back, with a new kolo-id and new logs; the others see a new member. The example never deletes anything without this confirm.

The founder settles changes to the member and room lists. With another admin, the plan offers "Hand over to" that admin: Leave makes it the settler and waits, with Cancel, until it took over, which needs it online, and only then deletes the folder. Without another admin, changes to the lists stay pending for everyone after the founder leaves.

An admin can remove a member from the member list (see [shared-state.md](shared-state.md#removing-a-member)). That is not revocation: the topic, the hello proof, the seal and the entry keys all come from the conversation key, so a removed member who keeps it can still read new messages from the pins. Keeping someone out for good means a new conversation key for every epoch, given to the members who stay, which the chat does not have.

## Invites

1. **Create.** An admin picks an expiry and a number of uses; every request needs an admin's approval. `Inviter.Create` returns the code: a 92-character string with a fingerprint of the inviter's kolo-id and a secret, never an address. The invite lives in the `invites/` store of the conversation's folder, so a restart keeps its expiry, its uses left and its revocation.
2. **Show the code once.** kolo-invite returns the code only from `Create`; `List` has no code. After a restart the invite still works and can be revoked, but its code cannot be shown again. Create another.
3. **Find the inviter.** Each engine runs `Inviter.Announce` next to its swarm and its event loop, which announces the topic of every live invite. The joiner joins the topic derived from the code, finds the inviter and calls `invite.JoinConn` on that connection. The first lookup of a topic can take up to `-lookup-interval` (5 seconds by default).
4. **Ask.** `JoinConn` sends the request, encrypted (HPKE) and signed with the joiner's kolo-id, with the joiner's signed log header and the braid's join request as join data (at most 6400 bytes). An admin sees the request and clicks Approve as member, Approve as admin or Deny: the role stays for good. Approve writes the joiner's admission in the braid and answers with the conversation and the braid's welcome; if the invite cannot carry it any more, the joiner is removed again. The request shows the first characters of the joiner's kolo-id, since the joiner has no profile yet.
5. **Become a member.** The joiner checks the answer, leaves the invite's topic, completes its braid with the welcome, saves `conversation.json`, issues its own capability for each pin and joins the conversation's topic. Its inviter and the founder serve it the braid at once; it talks to nobody until its braid holds the member list with its own admission. From then on it is the normal chat.

A request waits for a person, so an admin must be online. Nothing about pending requests survives a restart; the joiner can ask again, and asking twice is safe. An ended invite stays announced for 5 minutes (kolo-invite's `Linger`) so it can still answer "expired" or "revoked". A revoked invite stops working at once, also for requests that wait. The expiry is checked on the inviter's clock only.

Only admins create invites, approve and deny. A conversation has at most 64 members: Approve at 64 is refused before anything is written.

When a join fails, `Join` returns an `*InviteError` whose `Message` is the sentence for the window and whose cause is a kolo-invite error, so `errors.Is(err, invite.ErrExpired)` works: `ErrDenied` ("An admin denied your request to join."), `ErrExpired`, `ErrUsedUp`, `ErrRevoked`, `ErrInvalid`, `ErrInvalidCode`, `ErrRateLimited`, `ErrBusy`, `ErrUnreachable` and `ErrNotAnswered`.

## Connections

Each engine's loop (`route` in `chatservice.go`) gives every connection to one protocol: a peer that shares the conversation's topic gets chat and sync, and a peer that shares only an invite topic gets `Inviter.ServePeer`. Both `ServePeer` and kolo-sync's `Serve` accept streams, and [kolo-swarm](https://github.com/ciranda-dev/kolo-swarm) does not tell a peer that the other side joined a topic later on a connection. So the example decides at connect time, and a joiner leaves the invite's topic before it joins the conversation's: the invite-only connection closes and the conversation gets a new one. A peer of the conversation's topic gets sync only after its hello proves the conversation key and names our braid, and is talked to only when it is on the member list; one that sends no hello in 10 seconds (`sendTimeout`) gets the invite protocol. The chat and the braid share the connection by a tag byte at the start of each stream (`mux.go`, see [shared-state.md](shared-state.md#one-connection-for-the-chat-and-the-braid)). That is someone who left and joins again: kolo-dht keeps their old announcement of the topic for up to 20 minutes, so the topic alone does not tell.

A peer's public key and log ID come from the hello on the connection, or, for the inviter and a joiner, from the invite. A connection is direct, or goes through a [connection relay](configuration.md#connection-relays) when the direct one fails; the example uses both the same way, since kolo-sync takes either (its `Conn` interface). The key every check uses is the one the connection proved end to end, also through a relay.

## The window

The window is one surface, edge to edge, with one list of rooms: a room is a conversation of the engine, and the window shows its default room, `general`, and no other level. With no room it shows the box for an invite code, with Cancel while it waits. Otherwise it shows:

- on the left, the Kolo mark and a button that folds the sidebar into a rail of avatars (the choice is kept in the window's local storage, and the window works without it); under Rooms, a + that opens a menu with Create a room and Join a room, each opening a dialog with its form; the rooms as rows, each with a round avatar (the first letter of its name on a color taken from its ID), its name, its last message, the time of that message and its unread count; the list scrolls while the header and your card stay; one quiet line when the room has no pin; and your card with your name in the room, the start of your kolo-id and Settings;
- on top of the main column, the name of the room, how many members it has and how many are online (the peers connected now), and the buttons Invite (admins only: create an invite with an expiry and uses, copy its code, see and revoke the invites), Members and Profile (a small form for the name and the avatar);
- Members opens a panel beside the messages: the people waiting to be let in, with Approve as member, Approve as admin and Deny (a count shows on the button), and the member list with avatars, names, admin and pending marks and, for an admin, Remove after a confirmation that says what removal does not do;
- the messages, mine on the right and the others on the left, in groups and under day chips (below);
- the state of the member list, when it needs a sentence (paused with a member, waiting for another member, update Kolo Chat, and the like);
- the message box with Send, and under it one line: the messages are encrypted and go straight between the members' computers.

**Groups.** A message starts a new group when its author is not the author of the message before it, when more than five minutes passed since that message, or when the day changed. Inside a group the author shows once above the first message, each message has its own bubble with a small gap, and under the group show the time of its last message and, once, where its messages came from ("synced from pin" or "synced from peer"; a direct message shows nothing). A chip says Today, Yesterday or the date when the day changes, and before the first message. The time is the sender's clock, which `chat:msg` and `chat:history` carry as `time`; a live message that has none is placed at the time it arrived, and a history message that has none has no time and never breaks a group.

Profiles and members have their own page: [profiles-and-rooms.md](profiles-and-rooms.md).
