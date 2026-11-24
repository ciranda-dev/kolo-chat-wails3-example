# Tutorial steps

This repository is the finished chat of the tutorial "Build a desktop chat app" in the Kolo docs. The steps of the tutorial are in the starter, [kolo-chat-wails3-starter](https://github.com/ciranda-dev/kolo-chat-wails3-starter), on its `steps` branch: one tag per step, `step-2` to `step-8`.

| Tag in the starter | What the step adds |
|---|---|
| `step-2` | the chat, with direct delivery between peers |
| `step-3` | messages for peers that are offline, kept by a pin |
| `step-4` | invites with [kolo-invite](https://github.com/ciranda-dev/kolo-invite) |
| `step-5` | profiles and rooms with [kolo-kv](https://github.com/ciranda-dev/kolo-kv) |
| `step-6` | several conversations in one window, one folder and one engine each (see [how-it-works.md](how-it-works.md#several-conversations)) |
| `step-7` | the member list and the room list of each conversation in a [kolo-braid](https://github.com/ciranda-dev/kolo-braid) braid (see [shared-state.md](shared-state.md)) |
| `step-8` | the public network |

To see the code of one step, clone the starter and check out its tag:

```sh
git clone https://github.com/ciranda-dev/kolo-chat-wails3-starter
cd kolo-chat-wails3-starter
git checkout step-3
```

This repository (`main`, tag `v0.1.1`) is the finished app. It also has the tray and the notch window (see [notch-and-tray.md](notch-and-tray.md)), which the tutorial does not build.

The repository has no `go.work` and no `replace`, so build with `GOWORK=off` when this folder sits inside a Go workspace.
