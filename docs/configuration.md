# Configuration

Every setting is a flag or an environment variable; a flag wins, and for a list the flags replace the variable. Nothing is built into the code except the defaults below. A bad or missing setting makes `Start` fail with a `*ConfigError` (`errors.Is(err, ErrConfig)`; `ErrMissing` or `ErrMalformed` tell why, and `Setting` names the flag), and the window shows it.

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `-data` | `KOLO_CHAT_DATA` | `<user config dir>/kolo-chat` | the data folder, with one folder per conversation (see below) |
| `-bootstrap` | `KOLO_CHAT_BOOTSTRAP` | none: the public network | `host:port` of the entry nodes of your own network, comma-separated, at most 16, each an IPv4 address or a name that resolves to one. When set, they are used alone and the public entry nodes are not contacted (see below) |
| `-listen` | `KOLO_CHAT_LISTEN` | `127.0.0.1:0` when every `-bootstrap` entry node is on loopback, else `0.0.0.0:0` | local IP:PORT every conversation listens on. Each conversation has a socket of its own, so a port other than 0 allows one conversation only |
| `-conversation-key-file` | `KOLO_CHAT_CONVERSATION_KEY_FILE` | none | a file that holds the conversation key (32 bytes in hex, such as `openssl rand -hex 32`), readable by its owner only (mode 0600; a file open to other users is refused on macOS and Linux). For a founder who starts a conversation from the command line, generated once. Its folder keeps the key, so later starts can leave it out. Leave it out to start conversations in the window or join with an invite |
| none | `KOLO_CHAT_CONVERSATION_KEY` | none | the key itself, in the variable. There is no `-conversation-key` flag: a flag shows in the process list and the shell history, so the example refuses it and names the variable and the file. The file wins over the variable |
| `-pin` | `KOLO_CHAT_PIN` | none: optional | pins as `IP:PORT@KEY`, KEY being the pin's public key in hex (`kolo-pin id`). Comma-separated, and the flag is repeatable. They are the pins of every conversation you start, and keep messages for people who are offline; without them a conversation works only between members who are online (see below). A joiner gets the list from the invite |
| `-pin-file` | `KOLO_CHAT_PIN_FILE` | none: optional | a file of pins (see below). The flag is repeatable; the variable takes one path. A file that lists no pin is refused |
| `-pin-token-file` | `KOLO_CHAT_PIN_TOKEN_FILE` | the example issues one per pin | files of capabilities in hex for the conversation of the key, each signed by its kolo-id for one of the pins; whitespace or commas between tokens, `#` starts a comment. Readable by their owner only. The flag is repeatable; the variable takes one path. Only with a conversation key |
| none | `KOLO_CHAT_PIN_TOKEN` | | the tokens in the variable, comma-separated. There is no `-pin-token` flag, for the reason of the key. The files replace it |
| `-pin-ttl` | `KOLO_CHAT_PIN_TTL` | `720h` | lifetime of the capabilities the example issues |
| `-pin-quota` | `KOLO_CHAT_PIN_QUOTA` | `16777216` | bytes each pin may keep for you, in the capabilities the example issues |
| `-max-pins` | `KOLO_CHAT_MAX_PINS` | `16` | the most pins accepted, from the configuration and from an invite |
| `-lookup-interval` | `KOLO_CHAT_LOOKUP_INTERVAL` | `5s` | time between lookups of the topic |
| `-max-conversations` | `KOLO_CHAT_MAX_CONVERSATIONS` | `64` | the most conversations (rooms in the window) the example keeps, from 1 to 256, the join that waits for an answer included. Create and Join past it fail with `ErrTooManyConversations`. At start only that many run, the `-conversation-key` one first, then the oldest; the others are listed as not started, nothing in their folders changes, and a Leave starts the oldest that waits |
| `-relay` | `KOLO_CHAT_RELAYS` | none | connection relays as `host:port@key`, KEY being the relay's public key in hex. Comma-separated, and the flag is repeatable. The variable is read only when neither `-relay` nor `-relay-file` is given (see below) |
| `-relay-file` | | none | a file of connection relays, one or more `host:port@key` per line. Repeatable |

## Networks

Without `-bootstrap` the example joins the public network: it starts from the entry nodes built into Kolo, which Ciranda runs, free, with no uptime guarantee while Kolo is a beta. Settings says so in a sentence and shows no address; with `-bootstrap` it lists your entry nodes. Two people on different networks find each other with no setup, and the example listens on `0.0.0.0:0` to be reachable.

```sh
./bin/kolo-chat
```

With `-bootstrap` the example joins your own network, and only that one. List one entry node, or several for a network that should survive the loss of one:

```sh
./bin/kolo-chat -bootstrap 203.0.113.5:7447
./bin/kolo-chat -bootstrap 203.0.113.5:7447,node2.example.org:7447
KOLO_CHAT_BOOTSTRAP=203.0.113.5:7447 ./bin/kolo-chat
```

To use one computer only, run a kolo-node with `-first` on loopback and give its address:

```sh
kolo-node run -listen 127.0.0.1:7447 -identity /tmp/kolo-chat-demo/node/node.key -first
./bin/kolo-chat -bootstrap 127.0.0.1:7447
```

A bad entry (no port, port 0, IPv6 in any form, more than 16) stops `Start` with a `*ConfigError` for `bootstrap` (`ErrMalformed`). If no entry node answers, the room still starts and works from its own log: its state is `offline`, and it asks the entry nodes again by itself, waiting 2 seconds, then 4, and so on up to 30, each wait cut to a random value between its half and its whole, until one answers. The log says why with a `*NetworkError` (`errors.Is(err, ErrNetwork)`, and `kolodht.ErrNoNodes` inside) whose text names the three ways to connect, and the window shows the same sentences under the room, in English, Spanish or Portuguese; a join with an invite, which needs the network, returns that error. The example never tries another network. `Start` returns the network in use in `AppInfo.Network`, and Settings shows it with its entry nodes. Connection relays and pins are separate settings, both optional, and no public one exists: a relay is needed only behind strict NATs, and you run your own.

`-pin` and `-pin-file` without a conversation key are the pins of the conversations you start in the window. A conversation key needs no pin. A pin token without a conversation key is refused (`ErrMissing` for `conversation-key`): a token is for that conversation only, and it needs the pin it names (`pin.ErrUnauthorized` for `pin-token` when that pin is not in the list).

With a conversation key (`KOLO_CHAT_CONVERSATION_KEY` or `-conversation-key-file`), the example starts the conversation folder whose `conversation.json` holds that key, or makes one, named "Conversation", the first time. A later start with the same key uses the same folder; leave the key out and the folder still starts, as every other one does. A folder that lost its `conversation.json` is not found by its key, and its own start refuses it with a `DataDirError` that names it: move it aside and join again.

## The data folder

```text
<data>/                 0700
  layout.json           {"layout":3}, written once
  notch.json            the notch settings
  conversations/        0700
    <id>/               0700, one per conversation; the ID is random, never from the key
      identity.key      your kolo-id in this conversation
      local.json        its name, which only you see, and when it was added (0600)
      conversation.json its key and its pins (0600); missing while a join is in progress
      members.json      the members you know, with the signed headers of their logs and how many of their messages the window has shown
      directory-log     the ID of your directory log
      store/            the messages log, the directory log and the copies of other members' logs
      invites/          the invites you created
      pin-tokens/       the capabilities you issued, one file per pin, named by the pin's key (0600)
      braid/            the braid: the member list and the room list (see shared-state.md)
      state-flags.json  the members whose entries the member list refused (0600)
```

The data folder and `conversations/` must be private: one that other users can open (any group or other permission bit) is refused with a `*DataDirError` (`Open`) that names it; run `chmod 700` on it. Every file the example reads there is read only as a small regular file: a link, a device or a FIFO as `layout.json` is refused, and as `local.json` gives the name "Conversation".

A `layout.json` that names a layout the example does not know, or a damaged one, is refused at start with a `*DataDirError` (`Layout` is that number, or -1 when damaged) that names the folder, and nothing in it is changed: use another `-data`. Never restore or copy a conversation folder: see [shared-state.md](shared-state.md#the-settler).

If `conversations/` was deleted by hand, the example makes it again, empty and private. A conversation folder that cannot be used (logs that do not use the derived entry keys, a damaged `conversation.json`) does not stop the others: its row in the list shows why and which folder to move aside. A folder without `conversation.json` is deleted at start only when it is provably a join that did not finish: it holds nothing but its name, a kolo-id with its two stores, its own logs with no entry, its invite store and `directory-log`; no braid was completed in it; and its store holds no copy of another member's log. A kolo-id without both stores is refused too: that key may be on a member list. Any other folder without `conversation.json`, such as a member's folder that lost the file, its braid or `members.json`, is refused with a `DataDirError` that names it, and nothing in it is deleted. Move it aside and join again. A `conversation.json`, `members.json` or pin token that is not a regular file (a link, a FIFO, a device) is refused without being opened, so a start never hangs on one.

## What each conversation costs

Each conversation (a room in the window) runs its own engine: one UDP socket, one kolo-dht node, one swarm, two Pebble stores (`store/` and `invites/`), and its loops, so the bound is a safety limit for the example and not something to plan around. Eight idle conversations took about 290 goroutines, 5 MiB of heap and 140 open files in the test that measures them (`TestIdleCostOfEightConversations`). Sixty-four, the default bound, took 15.6 s to create one after another, then about 4,400 goroutines, 61 MiB of heap after a GC and 1,600 open files, with a peak resident size of about 440 MiB for the whole test process, on one Mac with loopback only and an entry node whose per-IP request limit was raised (`TestIdleCostOfSixtyFourConversations`, which runs only with `KOLO_CHAT_MEASURE_64=1`).

Each conversation has its own kolo-id, so:

- **Relays**: each conversation holds its own place at each relay. A relay run with kolo-node's defaults lets one address hold 4 connections, places and sessions together, so with more than 3 conversations run the relay with a higher `-relay-max-conns-per-ip`.
- **Allow lists**: a pin run with `-owners` and a relay run with `-allow-file` must list the kolo-id of each conversation. Settings shows it for the selected room, under "Your kolo-id in this room".
- **Pins**: a pin run with kolo-pin's defaults lets one address hold 4 connections at a time. All conversations push and pull at start, so with more than 4 on one pin some wait or fail for a moment: a push is tried again at the next message, and the pull at start at the next start.
- **The DHT**: a DHT server allows 50 requests per second per IPv4 address, shared by all the conversations of one computer, and a room puts about ten on it when it starts. Started together, the first 30 or so conversations reached the entry node and the rest got "no server answered". The example therefore starts its rooms four at a time (`startSlots`), each place waiting 1.5 seconds plus up to 1 second at random after its engine has joined its topic (`startPause`, `startJitter`), the room on screen first and then the newest, and a room that gets no answer asks again with a growing wait (2 to 30 seconds, `netRetryMin`, `netRetryMax`). These are constants of `netstate.go`, not settings. `TestFortyRoomsStartGently` starts 40 real engines against an entry node of its own that allows 50 requests a second per address: every room ends up online, none is dropped by the limit, and the most the node got in any second stays under it (`TestFortyRoomsAtOnceHitTheLimitAndRecover` shows the same start without the pacing dropping requests and recovering by retries). On loopback, 40 rooms were all online 21 s after the window opened (`Start` returned after 2.7 s); the entry node saw at most 24 requests in a second while they started and 15 once they were up, which is the rooms' own lookups every `-lookup-interval` (5 seconds by default), and dropped none.

## Pins

A pin is optional. It keeps an encrypted copy of your logs for the members who are offline, and you run it yourself: there is no public pin. You can list several: every message goes to each one, and a member who opens the example catches up from all of them, so a pin that is down or behind costs nothing while another one works.

```sh
./bin/kolo-chat -pin 127.0.0.1:7450@KEY1,127.0.0.1:7451@KEY2
./bin/kolo-chat -pin 127.0.0.1:7450@KEY1 -pin 127.0.0.1:7451@KEY2
KOLO_CHAT_PIN=127.0.0.1:7450@KEY1,127.0.0.1:7451@KEY2 ./bin/kolo-chat
./bin/kolo-chat -pin-file pins.txt
```

A pin file holds one or more `IP:PORT@KEY` per line, comma-separated; `#` starts a comment, and blank lines, a UTF-8 BOM and CRLF line ends are fine. It must be a regular file of at most 64 KiB.

```text
# the pins of our team
127.0.0.1:7450@KEY1
127.0.0.1:7451@KEY2   # the second one
```

The pins of `-pin` come first, then those of each `-pin-file`, in order. A pin listed twice the same way counts once; the same key at two addresses is refused. Addresses are IPv4 with a port from 1 to 65535. More than `-max-pins` pins is refused (`ErrTooMany`); the default, 16, is the bound Kolo's node uses for a list of peer addresses. Settings shows the pins in use, each with its state; they are set at start, not in the window.

Each pin listens on the address `kolo-pin run` gives it (`127.0.0.1:7450` unless told otherwise). To accept only your people, start it with `-owners` and list the kolo-id of every member, joiners included. Each conversation has its own kolo-id: the terminal prints it when the conversation starts, and Settings shows it.

```sh
go run github.com/ciranda-dev/kolo-pin/cmd/kolo-pin run -data .data/pin -max-bytes 100MB -owners ALICE_ID,BOB_ID
```

A pin capability is signed by the owner of a log, names one pin and covers that log only, and kolo-pin has no way to issue one for another member. So the invite cannot carry a usable pin token: it carries the list of pins, addresses and keys (an empty list when there is none), and the joiner issues its own capability for each pin. The quota is per member and per pin. A joiner whose kolo-id is not in a pin's `-owners` is refused by that pin and uses the others.

The example issues its own token for each pin and your two logs (the messages and the directory), keeps it in `pin-tokens/`, and reuses it until half of its lifetime has passed or you change the quota. Then it issues a new one, which replaces the old one on that pin at once. A token you give with `-pin-token-file` or `KOLO_CHAT_PIN_TOKEN` goes to the pin it names; the other pins get one from the example.

A token that cannot work stops `Start` with a `*ConfigError` for `pin-token` that wraps a kolo-pin error: `pin.ErrInvalidCapability` (not a capability), `pin.ErrUnauthorized` (issued by another identity, for a pin that is not in the list, or for a list of logs that does not include both of yours: leave `Logs` empty, which means every log of the owner, or list both) or `pin.ErrExpired`. Two tokens for one pin are `ErrMalformed`. Each pin checks its token again on every push, and its answers are `pin.ErrExpired`, `pin.ErrUnauthorized`, `pin.ErrRevoked`, `pin.ErrQuotaExceeded`, `pin.ErrDiskFull` and `pin.ErrUnreachable`. A pin that fails never stops the chat: messages still go directly, and the window tells you why.

Without a pin, a conversation has no token, no `pin-tokens/` folder and nothing to push or pull; its invites carry no pin; the window shows no pin error and the notch and the tray show no pin state. Members who are online exchange messages directly, and each pulls the other's log when they connect, so a message reaches an offline member once its sender and that member are online together. Nothing keeps a message for a member while the sender is offline too. The window tells you so: a note under New conversation when the example has no pin, and a note in each conversation that has none (`AppInfo.HasPins` is false when the example has no pin).

A pin works when its last push and the pull at start both did. The window shows an error, and the notch and the tray "pin unavailable", only when no pin works. When some do not, it shows a warning that says how many, with the reason for the first one, and the notch and the tray still say "pin ready": the others keep your messages. The `chat:pin` event lists every pin with its own push and pull sentence.

- **Expired token.** From its expiry the pin refuses new pushes (`pin.ErrExpired`) and deletes your log, with the check it runs every minute. The window names the pin. Nothing else stops: the other pins and direct delivery work, and the log on your machine keeps everything. Restart the example to issue a new token (or set a new token); the pin then pulls the whole log again.
- **Quota full.** The pin refuses a push that would take you past the quota (`pin.ErrQuotaExceeded`) before it stores anything, and never evicts what it has. What is pinned stays; new messages are not pinned there until you raise `-pin-quota` and restart. The pin has its own limit too (`-max-bytes`): then the error is `pin.ErrDiskFull`.

### Privacy

More pins means more parties holding your ciphertext. Every pin in the list keeps an encrypted copy of your messages log and your directory log until its capability expires. None of them can read a message, a room, a name or an avatar, but each one learns what a single pin learns: your public key, your log IDs, how many entries you write and when, their sizes, and the address you push from. With several pins, several operators learn it, and any of them may keep it after the expiry. The invite gives the whole list to every joiner, and each joiner pushes its own logs to every pin in it, so the list is a choice for the whole conversation. See [security.md](security.md).

## Connection relays

A connection relay carries the connection between two members that cannot connect directly, such as two computers behind carrier-grade NATs. Without relays the example connects directly only, as before. With them, kolo-swarm tries a direct connection first and the relays when it fails; the example then uses the relayed connection the same way: the invite and its grant, the hellos, the messages, and the sync of the history and of the directory (names, avatars, rooms) all go through it.

```sh
./bin/kolo-chat -relay 203.0.113.7:7448@KEY1
./bin/kolo-chat -relay 203.0.113.7:7448@KEY1,relay.example.org:7448@KEY2
KOLO_CHAT_RELAYS=203.0.113.7:7448@KEY1 ./bin/kolo-chat
./bin/kolo-chat -relay-file relays.txt
```

A relay file has the form of a pin file: one or more entries per line, `#` starts a comment, and it must be a regular file of at most 64 KiB. `-relay` and every `-relay-file` make one list, at most 16 relays; an entry listed twice counts once, and one key at two addresses or one address with two keys is refused. The host is an IPv4 address or a name that resolves to one; IPv6 in any form is refused. A bad entry stops `Start` with a `*ConfigError` for `relay` or `relay-file` (`ErrMalformed`; a value from `KOLO_CHAT_RELAYS` says so), and a file that cannot be read keeps its own error, such as `fs.ErrNotExist`.

Run a relay with kolo-node on a host every member reaches, on UDP port 7448; it prints its key when it starts:

```sh
kolo-node relay -listen 0.0.0.0:7448 -identity "$HOME/kolo-node/relay.key"
```

Every member lists the same relay: two members meet only through a relay both list, and the invite does not carry the list. Each conversation holds a place at each relay (a reservation) once you are in it, so the others can reach it there; a joiner reaches its inviter through the inviter's place. With relays, a join looks for the inviter for 195 seconds instead of 60: kolo-swarm tries the relays only after kolo-dht's connect failed, up to 3 x 45 seconds, and one relay dial takes up to 60 seconds.

Settings lists each relay with its state, and which setting gave the list (`-relay`, `-relay-file` or `KOLO_CHAT_RELAYS`). A relay that fails also shows in the conversation, and the terminal logs it once (`relay ADDR: ...`):

- **answered with another key**: the key after `@` is not the relay's. The example keeps trying it; fix the key and restart.
- **its key cannot be used**: the example stops using that relay.
- **refused this example**: the relay runs with an allow list of keys that may hold a place, and yours is not in it.
- **is full**, **cannot be reached**: the example asks again by itself.

A place at a relay lasts an hour by default and is renewed: Settings shows the relay as waiting for that moment, and nothing else changes.

kolo-swarm reports a wrong key only for a place it holds, which a joiner does not until it is in a conversation. So the example also checks each relay's key once at start, for all its conversations: it dials the relay with a throwaway key and only proves the relay's key with the handshake, which asks for nothing and reserves nothing. A relay that answers with another key shows at once in every conversation, also with none yet and in a join that starts later.

The relay passes bytes that are encrypted end to end between the two members (kolo-secure), so it reads no message, name, room, conversation key or entry key. It learns the kolo-ids of the members that hold a place or connect through it, their IP addresses, who connects to whom, when, and how many bytes. kolo-node's relay limits each connection by default (`-relay-max-session-bytes`, `-relay-max-session-duration`, `-relay-rate-bytes`); a chat moves little data, and a connection that ends is made again.

A pin is never reached through a relay: it needs an address every member reaches, as the entry node does.

## Invites

An invite lasts from 1 second to 30 days (one day when you give no expiry) and takes 1 to 100 uses (one when you give none). You can have 16 active invites at a time in each conversation: each one takes a topic of the swarm, which the example opens with room for them (kolo-swarm's largest `MaxTopics`, 32). Give invites a short expiry.
