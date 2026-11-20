# Troubleshooting

| You see | What to do |
|---|---|
| "Could not reach the network, trying again" (under the room's name, with a red dot in the list) | No entry node answered: the room keeps working from its own log, keeps what you type, and asks again by itself every few seconds, up to every 30. Nothing to do but fix the network. The note under the messages says which: "Kolo Chat could not reach the public network" means no public entry node answered, so check your internet connection and that UDP is allowed out (the public network has no uptime guarantee while Kolo is a beta); "Kolo Chat could not reach your network" means no entry node of `-bootstrap` answered, so run `kolo-node check <address>`, which must print `ok`, and fix the address or start the node. The example does not use the public network in place of your own. |
| "Connecting..." (a dot in the list) for a while after the example opens | The example starts the rooms four at a time so the entry nodes, which let one address send 50 requests a second, are not flooded: 40 rooms took 21 seconds to come up in the test on loopback, so 64 take about half a minute; the room on screen and the ones with the newest messages come first. Open a room and it starts next. Nothing is lost while it waits. |
| "Reconnecting..." | The last connection to a peer ended, because the peer closed the example or the network changed. The room looks for it for 45 seconds, then says "Nobody else online". What you typed is kept and the peer shows it, once, when it is back. |
| You typed while offline and the other side has nothing yet | It arrives when the connection returns, from you if you are online together or from the pin if you have one. The log of the room keeps it across a restart. |
| `kolo-chat: -bootstrap is malformed` | Write each entry node as `host:port`, with an IPv4 address or a host name and a port from 1 to 65535, comma-separated, at most 16. |
| `kolo-chat: -conversation-key is required` | You set `KOLO_CHAT_PIN_TOKEN` without a key: a token is for the conversation of the key only. Unset it, or set the key. |
| `kolo-chat: -pin-file is malformed: <path> lists no pin` | The file has no `IP:PORT@KEY` entry, only comments or blank lines. Add a pin or leave `-pin-file` out: the pin is optional. |
| "No pin: messages reach people only while they are online" (the line under the room list) | Not a fault: without a pin, messages reach people only while they are online. To keep them for people who are offline, run a pin and start the example with `-pin` or `-pin-file`. |
| A member who was offline does not get a message | Without a pin it comes when the sender and that member are online together. A pin (`-pin` or `-pin-file`, run by you) keeps it for them even when the sender is offline. |
| "You have the most rooms this example keeps" | Leave one, or start the example with a larger `-max-conversations` (default 64, at most 256). A join in progress counts. |
| `kolo-chat: -listen has a fixed port, which only one conversation can use` | Each room has its own socket. Use port 0 (`-listen 0.0.0.0:0`), or keep one room. |
| `The data folder ... holds one conversation of Kolo Chat 0.1.9 or older` | This version keeps each conversation in a folder of its own and does not migrate. Quit the example, move or rename the folder the message names (nothing in it is deleted), open the example again, and start a new conversation or paste a new invite. |
| `The data folder ... was made by Kolo Chat 0.1.10, whose conversations have no shared member list` | This version keeps the member list in a braid every member shares, and 0.1.10 conversations have none. Quit the example, move or rename the folder (nothing in it is deleted), open the example again, and start a new conversation or paste a new invite. Every member needs this version. |
| "Members and rooms are not up to date with N member(s) right now" | That member's braid broke the protocol, so the example paused the member list with it (10 minutes, doubling up to 160). The chat works. When the pause ends the connection starts again. |
| "Waiting for another member to come online." | After a start your copy of the member list needs another member to confirm it before you change it. Try again once someone is online. |
| "Members are online but none answers" | Your kolo-id may have been removed from the member list. Ask an admin; an admin can approve a new request with an invite you still have. |
| "This computer has another member list for this conversation" | Someone started the conversation with the same conversation key as you: that makes a second member list. Leave it and join with an invite. |
| "A newer Kolo Chat changed the member or room list" | Update Kolo Chat. Until then this example does not learn of later changes to the member list, removals included. |
| "Only an admin can do that." | Inviting, approving, removing and adding rooms need an admin. Roles are chosen when a request is approved and do not change. |
| `The data folder ... was made by another version of Kolo Chat (layout N)` | Use that version, or start with another `-data`. |
| `The data folder ... is open to other users` | Run `chmod 700` on the folder the message names, or start with another `-data`. |
| A room shows "You have the most rooms this example keeps" and does not start | It is past `-max-conversations`: only that many start. Leave one and it starts at once, or raise the setting; until then its folder is left as it is, and it cannot be renamed. |
| A pin or a relay with an allow list refuses one room only | Each room has its own kolo-id: add each one to the pin's `-owners` or the relay's `-allow-file`. Settings shows it under "Your kolo-id in this room". |
| A relay says "is full" with several rooms | Each room holds its own place: run the relay with a higher `-relay-max-conns-per-ip`. |
| `kolo-chat: -pin is malformed` | Write each pin as `IP:PORT@KEY` with an IPv4 address. `-pin-key` is gone: the key goes after the `@`. |
| `kolo-chat: -pin lists too many pins` | List fewer pins, or raise `-max-pins`. |
| `kolo-chat: -relay is malformed` | Write each relay as `host:port@key` with an IPv4 address or a host name and the relay's 64 hex character key, at most 16 in all. |
| "Connection relay ...: answered with another key" | The key after the `@` is not that relay's: copy it again from the line the relay printed at start (`identity key=...`) and restart the example. |
| "Connection relay ...: refused this example" | The relay runs with `-allow-file` and your kolo-id is not in it. |
| Two examples behind strict NATs never connect, with a relay ready on both | Both must list the same relay: the invite does not carry the list. |
| "The invite lists more pins than this example accepts" | Start the example with a larger `-max-pins` and join again. |
| "Could not reach the person who invited you" | The inviter is offline, or has not announced the invite yet. Try again in a few seconds. |
| "This invite has expired", "...no uses left", "...was revoked" | Ask for a new invite. |
| "The pin at 127.0.0.1:7450 is unreachable" | Start the pin (terminal 2 of [Try it](try-it.md)). |
| "The pin at ... refused this identity or token" | That pin runs with `-owners` and your kolo-id is not in the list, or the key after the `@` is another pin's key. |
| "1 of 2 pins cannot be used now" | The other pins keep your messages. Settings shows which pin fails and why. |
| `conversation.json: made by an older version of Kolo Chat` | The conversation's folder joined with 0.1.1 or older, which kept one pin. Ask for a new invite, quit the example, move that folder aside, then open the example and paste the invite. The example never deletes the file itself. |
| `conversation.json: damaged` | The file is not one the example wrote. Move that conversation's folder aside and join again with a new invite. |
| "N messages could not be opened yet" | Storing their keys failed, usually on a full or failing disk; the log says `keys of ...`. The next pull, at start, on each connection or after a direct message, tries again. |
| A member shows "unchecked" | That member's example wrote a message whose key does not match its signed record, which only a broken or changed example does. That message is not shown; the member's other messages are. After more than 256 such entries the member is not read at all: once its example is fixed, it leaves and joins with a new invite, which gives it a new kolo-id (see [security](security.md#other-things-to-know)). |
| `The data folder ... was made by Kolo Chat 0.1.7 or older` | 0.1.8 derives the entry keys from the conversation key, and logs made before cannot change. Quit the example, move or rename the folder the message names (nothing in it is deleted), open the example again and paste a new invite; a founder starts a new conversation and invites everyone again. |
| `The data folder ... holds the logs of a conversation whose conversation.json is gone` | The folder lost `conversation.json` but still holds your messages, so the example cannot tell which conversation they belong to. Move or rename the folder as above and join again with a new invite. |
| Two examples never connect, and the log says `hello refused ... (Kolo Chat 0.1.7 or older)` | The other example is 0.1.7 or older. Every member needs 0.1.8 or later. |
| `cmd/bootstrap` fails with `address already in use` | Another process holds port 4000. |
| `key file or its directory is accessible to others` | The data folder or a conversation's folder is open to group or others. Use `chmod 700`. |
| You left a room and the example enters it again on the next start | The example was started with `-conversation-key-file` or `KOLO_CHAT_CONVERSATION_KEY`, which makes that room again. Take the key and the pins out of the flags and the environment; Leave room says so before you confirm. |
| "Confirm first: open Leave room again" | Leave needs the token of the latest plan, and each try uses it up. Open Leave room again and confirm. |
| A window shows "This window stopped on an error" | If the window shows an error pane, press Copy and see the terminal for the line starting with frontend error. Send both with a bug report, then quit the example and open it again. |
