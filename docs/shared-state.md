# Shared state: the member list and the room list

Each conversation keeps its member list and its room list in a [kolo-braid](https://github.com/ciranda-dev/kolo-braid)
braid. The code is in `state.go` (the apply function), `braid.go` (the braid of a conversation), `braidstatus.go` (what
the example adds: the window's state, the pause, the marks and the hand over) and `mux.go` (one
connection for the chat and the braid).

## Why a braid here

Messages need no agreed order. Each member writes its own log, and two messages written at the same moment can be
shown in either order without anyone losing anything: they commute. The member list and the room list are different:
they are shared state. Two admins can act at the same moment, one removing Carol while the other approves Dave, or
both adding a room called "design", and every member must end with the same lists. kolo-braid gives that: it merges
every member's entries into one order that every peer computes the same way, and an apply function you write turns
that order into the lists. The chat keeps its messages, profiles and offline delivery as they were, moves only the two
shared lists into a braid, and talks only to the people on the agreed member list.

What this list cannot do: an admin is chosen when a request is approved and stays one, the member who settles the
order (at first, the person who started the conversation) cannot be removed, and a removal takes effect in each
member's example only once that example has learned of it. Removing someone does not change the conversation key, so it is
not revocation: the removed person keeps the key and what they received. A removed member can still read new messages
and profiles from the pins, because every entry key comes from the conversation key.

kolo-braid is beta: its settlement and membership are model-checked within bounds, which is not a proof, and it has
been tested on one Mac, on 127.0.0.1 only.

## The braid of a conversation

- Its own store, `conversations/<id>/braid/`, and the conversation's kolo-id as its member key.
- Fixed in its genesis: one settler (the person who started the conversation), Majority, at most 64 members
  (kolo-braid's default) and bodies of at most 1 KiB (its least). `MaxPullAhead` is 128, twice the least, so a
  settler's next pulse is always in reach.
- Roles are chosen when an admin approves a request: "Approve as member" or "Approve as admin". They do not change
  later. Admins invite, approve, deny, remove members and add rooms. Admins are fully trusted, also with settlement.
- Every braid write runs under 10 seconds, kolo-braid's `ConfirmWait`. After every start a member's strand is
  unconfirmed until another member answers, so an admin that is not the settler and starts alone sees "Waiting for
  another member to come online." for a change.

## The apply function

A data entry of the chat is a fixed byte layout, not JSON: byte 0 the version, byte 1 the op, byte 2 the name's length
in bytes, then the name. Version 1 has one op, 1, "add a room". `applyState` checks, in this order:

1. Every kind but data returns nil: the library applied the membership effect.
2. A writer that is not an admin at the entry's position is rejected, whatever the version, so a member cannot halt
   anyone's view.
3. An empty body, or version 0, is rejected.
4. A version above 1 is an error, which halts this example's view until it is updated: whatever a later version adds
   comes after byte 0, so an old example never rejects it.
5. A version 1 body whose op is not 1, or whose length byte is not the rest of the body, is rejected.
6. A bad room name, `general`, a name already in the list (ASCII letters compared in any case, every other byte as
   it is) or a 65th room is rejected.

The view holds one key, `rooms`, the sorted list as length-prefixed names. Members are never copied into the view: the
example reads the roster from the braid. Names that look alike in another Unicode form can coexist. Rooms are never
removed or renamed. `state_test.go` runs the apply through `kolobraidtest.CheckApply`, `FindDivergence` and
`CheckUpgrade`.

## One connection for the chat and the braid

kolo-swarm keeps one connection per peer key, and the chat's messages, its kolo-sync server and the braid's
`ServeConn` each must be the only one that accepts streams on it. So every stream starts with a tag byte: `m` the
chat's messages (uni streams), `s` the chat's kolo-sync (bidi streams) and `b` the braid (both). Each accepted stream
gets its own goroutine, which reads the tag and hands the stream to its part; a wrong or late tag cancels that stream
only. Each connection allows 64 incoming streams per direction, so a busy braid never takes the credit the chat needs.

- Only `ErrNotMember` (a key unknown for 60 seconds) closes the connection: the swarm dials again and both braids
  start fresh. That is a joiner that met someone before its admission was ordered, or a removed member.
- A protocol fault of the braid, or a braid part with more than 32 streams waiting, pauses the braid with that member.
  The queue rule closes the part at once, so its streams free their credit and the chat goes on; the pause starts
  once the braid's reader lets go of the stream it holds, within kolo-braid's 60 second message timeout. The pause is
  10 minutes, doubling at each fault in a row up to 160 minutes, and back to 10 minutes after
  a pause served 10 minutes without a fault. The chat goes on, and the window says that members and rooms are not up
  to date with that member. When the pause ends the example closes the connection once, so both braids start fresh. The
  other side gets nothing from us until the pause ends; its window may not show the pause. Both values are not from a
  reference: 10 minutes is ten times the braid's 60 second answer timeout, and 32 is half of the 64 streams.
- A 0.1.10 example sends untagged streams, which this version cancels: versions do not mix.

## Proven and accepted

A hello is **proven** when it proves the conversation key and names our braid. A proven hello starts the bidi
streams, serves the braid and ends the wait for the invite protocol, also from someone who is not on the member list
yet, so a joiner can sync its own admission. A peer is **accepted** when it is proven and on the member list of the
braid's view. Only an accepted peer is registered, gets a reply hello, is sent messages, served, pulled from a member
or a pin, and shown. A joiner accepts nobody until its braid holds the member list with its own admission, which its
inviter or the founder gives it at once.

The accepted set lives in memory and is rebuilt from one snapshot of the view on every braid event, so a membership
event that kolo-braid dropped from a full queue changes nothing: the events after it rebuild from the whole roster. A hello from someone with the key but not on the list, such as a removed member under a new
kolo-id, is proven and never accepted.

## Removing a member

Does: every example that holds the removal stops talking to that member, serving it, pulling it and showing it, at once
when the removal is tentative. What it wrote before stays. The braid stops serving it once the removal settles, which
needs the settler online.

Does not:

- change the conversation key: a removed member keeps it, and can still read new messages and profiles from the pins
  and give the key to others;
- reach examples that have not learned of it: an offline member, or one that has not met an admin or the settler since,
  still talks to the removed member;
- tell the removed member, whose example may never learn it: it sees members online that do not answer;
- remove the settler (at first, the founder): the window offers Remove on nobody with the seat;
- end an invite the removed member still has: it can ask to join again, and an admin must approve it. The
  confirmation offers to revoke your active invites for this conversation.

Members that learned of a removal at different times keep different amounts of what the removed member wrote. To keep
someone out for good, start a new conversation without them.

## Admissions, joining again and the bound

- Approve checks the member bound first (64), then the braid's `Admit`, then the invite's answer, which carries the
  conversation and the braid's welcome. If the invite cannot carry it any more, the joiner is removed again.
- Two admissions at once past 64, or by an admin removed at the same moment, can give the key to someone whose
  admission is void. Each example talks to them until it holds the entry that makes it void; they keep the key.
- A join that fails or is cancelled deletes its folder, kolo-id included, and the next one starts with a new kolo-id:
  an admin may have admitted the old key already, and the braid admits a key once. At start, a folder without
  `conversation.json` is deleted only when it is provably a join that did not finish: nothing in it but a kolo-id with
  its stores, own logs with no entry, no completed braid and no copy of another member's log. Any other one, such as
  a member's folder that lost the file, is refused with the folder to move aside, and nothing in it is deleted (the
  exact rule is in [configuration.md](configuration.md#the-data-folder)). Such an old key can stay a member
  that never connects; an admin can remove it.
- A conversation key given by hand (`KOLO_CHAT_CONVERSATION_KEY`) starts a braid. Two people who both start with the same key have two member lists,
  and their windows say so: everyone else joins with an invite.

## The settler

The person who started the conversation is the only settler. Changes by other admins show at once and settle while
the settler is online; until then the window marks them pending. In Settings, the settler's Leave offers "Hand over to"
each other admin: the example writes a settler change to that admin and waits, with Cancel, until it took effect, which
needs that admin online; only then the folder is deleted. Without another admin, changes to members and rooms stay
pending for everyone after the settler leaves.

Never restore or copy a conversation folder: the settler's copy stops changes to the lists for everyone, and anyone's
copy can lose its strand ("This copy of the conversation can no longer write").

## What the window says

`chat:state` comes from the braid's events loop, never from a call that failed: pending changes and rooms, the paused
members, and one sentence each for a halted view ("update Kolo Chat"), settlement that stopped for good, a view that
differs from the others', a lost strand, a write that waits for another member, members online who do not answer, and
another member list for this conversation on this computer.

An app that a later version's entry halted stops following the member list too: until it is updated it does not learn
of later changes to the list, removals included (`TestHaltedAppKeepsItsMemberList`).

## Flooding

The braid has no budget per member. Bodies are at most 1 KiB, and a member's strand is fetched at most 128 entries
past what settled. The example never writes a data entry as a member who is not an admin, so a member on the list without
the admin role whose entries were refused is marked "wrote entries that were refused"; the mark and the position read
so far are kept in `state-flags.json` (0600). A member can make every peer store its refused entries for good; an
admin removes it.

## Offline

A member that was offline shows the lists it held, and from the pins the new messages of the members it knows. A
member admitted meanwhile shows once its braid syncs with a member online. A new member reads nothing, not even from
the pins, until its braid has synced once with a member online: until then it is not on its own member list, talks to
nobody, and its window says that it waits for a member. A message for a room it does not know yet
shows under that room's name, dimmed, and cannot be answered until the room list arrives. The braid is never on a pin:
kolo-pin keeps kolo-logs and does not run `ServeConn`.
