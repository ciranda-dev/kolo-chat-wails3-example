# Tutorial steps

This repository is the finished chat of the tutorial "Build a chat, step by step" in the Kolo docs. Each step is a git tag:

| Tag | What the step adds |
|---|---|
| `step-2` (also `v0.1.0`) | the chat, with direct delivery between peers |
| `step-3` | messages for peers that are offline, kept by a pin |
| `step-4` | invites with [kolo-invite](https://github.com/ciranda-dev/kolo-invite) v0.1.0 |
| `step-5` | profiles and rooms with [kolo-kv](https://github.com/ciranda-dev/kolo-kv) v0.1.1 |

`main` is past `step-5`. From 0.1.10 it keeps several conversations in one example, one folder and one engine each (see [how-it-works.md](how-it-works.md#several-conversations)); the tutorial's step 6 builds the same shape on top of `step-5`, with the tag `step-6`. From 0.1.11 each conversation keeps its member list and its room list in a [kolo-braid](https://github.com/ciranda-dev/kolo-braid) braid (see [shared-state.md](shared-state.md)); the tutorial's step 7 builds it on top of `step-6`, with the tag `step-7`. `v0.1.1` added the tray and the notch window, which change nothing on the network (see [notch-and-tray.md](notch-and-tray.md)). After it, `main` takes a list of pins: `-pin IP:PORT@KEY,...`, `-pin-file` and `-max-pins` (see [configuration.md](configuration.md#pins)), and its invites carry that list. The tags never change, so `step-3` to `step-5` and `v0.1.1` still take one pin with `-pin IP:PORT` and `-pin-key` (`KOLO_CHAT_PIN_KEY`), as the tutorial pages show. Follow a step with its tag, not with `main`.

To see the code of one step, check out its tag:

```sh
git checkout step-3
```

Step 5 adds one dependency:

```sh
go get github.com/ciranda-dev/kolo-kv@v0.1.0
```

The repository has no `go.work` and no `replace`, so build with `GOWORK=off` when this folder sits inside a Go workspace.
