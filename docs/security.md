# Security

## The conversation key

The conversation key is the secret that matters. Whoever holds it can read every message and profile of the conversation whose encrypted copy they can get, from a member or from any pin of the conversation, without the sender being online. Every entry key is derived from it (see `entrykeys.go`), so the example never sends entry keys. That includes everyone who ever joined: an admin can remove a member from the member list, but nothing rotates the key in this example, so a person who joined once, removed or not, can read every message sent to the conversation, before and after they joined, for as long as the pins keep them, even after they stop using the example. Nobody who gets the key later is kept out of earlier messages: there is no forward secrecy. The pin cannot read anything, because it never holds the key.

The entry secret is HKDF-SHA256 of the conversation key under its own label, `kolochat/entry-secret/v1`, so kolo-log never gets the conversation key itself. The conversation key must be 32 random bytes (`openssl rand -hex 32`): anyone who holds one encrypted entry can test a guess of the key offline, as anyone who watches the DHT already can with the topic, a hash of the key.

The key is never sent in the clear. It travels only inside the invite's answer, and it is kept only in `conversation.json` (mode 0600, in the conversation's folder, which is created 0700) and, on the inviter's side, in the invite's records in `invites/`, which kolo-invite keeps in the same folder. It is not in logs, errors, events or any other file.

Removing a member is not revocation. An admin takes a member off the member list (see [shared-state.md](shared-state.md#removing-a-member)): each member's example stops talking to it, serving it, pulling it and showing it once that example learns of the removal, and an app that is offline learns it later. The removed member keeps the conversation key: it can still read new messages and profiles from the pins, and give the key to others. Revoking an invite stops new requests, not those who joined. Leave conversation deletes that conversation's folder, with the key, the logs, the invites and your kolo-id in it, from your own computer only: the others keep you on the member list until an admin removes you, and keep what you sent. Keeping someone out for good needs a new conversation key per epoch, which the chat does not have: start a new conversation without them.

Admins are fully trusted, also with settlement: an admin can add and remove anyone but the settler, and can write a change of settler. Never restore or copy a conversation folder: the settler's copy stops changes to the member and room lists for everyone, and anyone's copy can lose its strand.

## What the invite carries

The answer to an approved request (the `grant` type in `invite.go`) is encrypted by kolo-invite for that person alone. It holds:

- the conversation key (32 bytes);
- the list of pins, each one's address and public key (empty when the conversation has no pin);
- the inviter's signed log header, which names the inviter's key and log ID: what the joiner needs to meet the inviter and pull its log from the pins;
- the braid's welcome, which makes the joiner's braid.

It does not carry a pin token (see [configuration.md](configuration.md#pins)). A joiner accepts at most `-max-pins` pins from an invite. The joiner sends its own signed log header and the braid's join request as join data, in the same encrypted request, at most 6400 bytes. Every request waits for an admin's approval.

The invite code is a secret too: only the answer of `CreateInvite` has it, and the example logs only the invite ID. `TestInviteJoinAndChat` checks that neither the key (hex or base64) nor a code shows up in the log or in any event. The pin never sees the key or the code.

## What the pin can see

Each pin in the list sees what follows (a conversation with no pin has none), so more pins means more parties holding your ciphertext and this metadata. Pick pins you trust as a group: the invite gives the list to every joiner, and every member pushes to every pin in it.

- which public keys and log IDs it hosts (two per member: the messages and the directory), how many entries each has, and their sizes. kolo-log pads every entry, so the real length of a message is hidden. The number of entries of a directory tells it how often a member changed its profile;
- when a log grows, and the IP address and timing of every connection;
- your capability: its expiry, its quota, and the logs it covers.

## What the pin cannot see

The pin cannot read a message, a room, a name or an avatar. It never gets entry keys (the example only uses `PullCiphertext` on its side, and the pin serves ciphertext only), and its entry keys are derived from the conversation key, which the pin never has. The pin also sees that each log derives its keys, and at which epoch (always 0 here).

Anyone who knows your public key and log ID can ask any pin that holds your logs for the ciphertext and learn the same metadata, so keep the pin addresses among the members, or run the pins with `-owners`.

## Someone who finds the topic

The conversation's topic is `SHA-256("kolochat/topic/v1" || key)`, announced on the DHT (the public network by default) by every member. Whoever looks it up, or watches the DHT, learns the topic and the IP addresses of the members that announce it. The swarm admits every kolo-id (`Allow` is not set), so a stranger who has the topic can open a connection to a member. A kolo-id is a key anyone can make; the connection proves the stranger's key, not that it is a member.

What a stranger gets from that connection:

- our hello, sent first: our signed log header (our kolo-id and log ID), the signed header of our directory log, the ID of our braid and a proof that is bound to both kolo-ids and useless without the conversation key. No name, text, room or member list;
- a connection that costs us a connection slot, a few goroutines and memory. kolo-transport's own worst case for one connection that sends one-byte frames on every stream it may open is `2 x 64 x 256 KiB x 96 = 3 GiB`, plus `512 KiB x 176 = 88 MiB` of unread data, about 3.1 GiB (the library's formula, in its `docs/limits.md`, not measured here; this example allows 64 incoming streams of each kind). One source address may hold 4 connections, about 12 GiB, and the swarm admits 32 inbound peers, about 99 GiB from 8 addresses. Memory held by hostile peers is live heap, so no limit frees it: the example can run out of memory until the system ends it;
- an inbound peer slot for `sendTimeout` (10 seconds): a connection that shares only the conversation's topic and sends no hello is closed then. A stranger that sends a hello that does not prove the conversation key is not registered and gets nothing. Strangers can keep opening connections, so while many do, members may connect later than usual.

What a stranger cannot do: read or add a message, a profile, a room or the member list; pull our logs or the braid (both need a hello that proves the conversation key, and our logs need the key to be on the member list); be shown in the window; or join (that needs the invite code, which is the secret of the invite topic). A stranger that has the conversation key but is not on the member list is proven but not registered: it gets the braid, which it needs to sync its own admission, and nothing else (see `TestKeyAloneIsNotMembership`).

Why the swarm's `Allow` is not set to admit only the members: it sees the kolo-id and the address, not the topic, so it cannot tell a joiner on an invite's topic, which must be admitted, from a stranger on the conversation's topic, and a member that an admin approved is not on every other member's list until their braids sync. It also turns on window growth for every connection it admits, which would give a stranger the larger windows whenever an invite is open and enlarge the numbers above. The defence is the hello's proof, the 10 second limit and the memory limit below.

## Memory limit

At start the example sets a soft memory limit with `debug.SetMemoryLimit`, unless `GOMEMLIMIT` is set: half of the physical memory, never below 512 MiB, and 2 GiB when the physical memory cannot be read (it is read on macOS, Linux and Windows). Set `GOMEMLIMIT` (for example `GOMEMLIMIT=3GiB`) to use another value; `GOMEMLIMIT=off` removes the limit. The limit is soft: when the heap nears it the Go runtime collects more often, so garbage is freed earlier, but heap that peers keep live is not freed and the limit does not refuse a connection. It cannot stop the worst case above; it keeps the example from growing for no reason.

## Other things to know

- **A new member reads history.** A joiner reads the whole history of every member on the member list whose log it knows, from a member or from the pins: its inviter's, which the invite names, once its braid holds the member list, and every other member's after that member's first hello. After that it reads each one from the pins, also a member who is offline or stopped using the example. kolo-log and kolo-sync have no way to start a log from a point.
- **Deleting would not take a message back.** The example deletes no message. If it did, deleting an entry could not take it from anyone who holds the conversation key and kept a copy of its ciphertext: a key that can be derived again cannot be destroyed.
- **No forward secrecy.** Whoever gets the conversation key, at any time, reads every message still on a pin or in a copy, past and future.
- **A member's example that writes wrong keys.** A record that commits to a key other than the derived one is refused by every member, since every member derives the same keys and no key travels. That message is not shown, the member list marks its sender "unchecked", and the sender's other messages still show. A sender with more than 256 refused entries in its two logs (messages and profile changes together) is quarantined: no longer pulled or read, even after it joins again with a new log. Records stay signed by their author, so nobody can write in another member's name.
- **The way back from a quarantine.** The quarantine is kept with the member's kolo-id in `members.json`, on each reader. The member gets back by leaving the conversation and joining again with a new invite, which gives a new folder and so a new kolo-id; a reader that leaves the conversation forgets it too. Why no other way: only the member's own signed records can quarantine it, so nobody else can silence an honest member, whatever logs it starts; and a way back that needs no new invite (a new log, a timer, a button every reader would have to press) would hand a broken or hostile example a new budget of 256 for free. A new kolo-id needs a new invite, which a member decides on.
- **The member list and the room list** are entries of a braid that every member keeps for good, signed and encrypted with the braid's own keys. A member who is not an admin can make every peer store refused entries; the window marks it and an admin removes it (see [shared-state.md](shared-state.md#flooding)). The braid is never on a pin.
- **The window runs under a content security policy.** Both pages (`frontend/index.html` and `frontend/notch.html`) carry `default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'`, so a script that got into a page by a future rendering mistake could not load code or call out, and the window has no `innerHTML`, `eval`, link or `fetch` today. `npm run lint` in `frontend/` fails when a page lacks the policy or has inline code, and `csp.test.js` runs the built pages in a headless Chrome and checks that nothing is blocked and that the policy is enforced. The dev server serves the pages without the policy, since hot reload injects inline scripts.
- **Connection relays** pass bytes encrypted end to end between two members: they read no message, name, room or key. They learn the kolo-ids that connect through them, their IP addresses, who talks to whom, when and how much (see [connection relays](configuration.md#connection-relays)).
- **Direct messages** are sealed with the conversation key and come over an authenticated connection. Each one is shown at once, and when the sync that follows brings the member's log, the entry at the same index is compared with the message that was shown. A member whose text differs (its example signed one text and sent another) is marked "sent a message that differs from the one it signed" in the member list, and the history shows the signed text after a restart. Up to 256 messages per member wait for the check; past that, more are shown and not checked. The mark is kept in `members.json` and ends when the member joins again with a new log.
- **One kolo-id per conversation.** Each conversation has its own kolo-id, made in its own folder, so the members of two conversations cannot link them through your kolo-id. Your IP address and the timing of your connections still can. The name you give a conversation is kept in its `local.json` and never sent.
- **Names are not identities.** Anyone can pick any name; see [profiles-and-rooms.md](profiles-and-rooms.md#names-are-not-identities).
- **The data folder** and each conversation's folder are created with mode 0700, and an existing one open to other users is refused. State files (`conversation.json`, `members.json`, `directory-log`, `local.json`, the notch settings and the pin tokens) are written to a temporary file that is synced to disk, renamed over the old one, and the folder is synced, so a power loss leaves the old file or the new one, never an empty one. The example logs conversation IDs, which are random and say nothing of the key, and never the contents of `conversation.json` or `local.json`.
