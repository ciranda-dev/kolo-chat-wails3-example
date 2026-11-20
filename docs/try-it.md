# Try it

The README's [Try it](../README.md#try-it) section has the commands, one per block. This page says what each part does, which ports to open and what to check when two examples do not see each other. Every flag is in [configuration](configuration.md).

## Which network

Kolo Chat has three ways to find the other peers. None of them needs an entry node you did not choose, and the example never falls back from one to another.

| Network | How | Entry nodes |
|---|---|---|
| Public, the default | no `-bootstrap` | the ones built into Kolo, run by Ciranda: free, with no uptime guarantee while Kolo is a beta |
| Your own | `-bootstrap host:port[,host:port]` or `KOLO_CHAT_BOOTSTRAP` | yours, from `kolo-node run`; the public ones are not contacted |
| This computer only | `-bootstrap 127.0.0.1:<port>` | a `kolo-node run -listen 127.0.0.1:<port> -first` on this computer |

If no entry node answers when the example starts, the window says so and names these three ways: check your internet connection for the public network, or that your entry nodes run for your own. Settings shows the network in use and its entry nodes.

## What runs where

| Part | Command | Listens on | Needed by |
|---|---|---|---|
| Entry node | `kolo-node run` | UDP 7447 | every example of your own network, through `-bootstrap`; not needed on the public network |
| Pin | `kolo-pin run` | UDP `127.0.0.1:7450` by default, `-listen 0.0.0.0:7450` for other computers | nobody: it is optional. With one, the founder gives it through `-pin` or `KOLO_CHAT_PIN`, and joiners get it from the invite |
| App | `./bin/kolo-chat` | UDP `0.0.0.0:0` on the public network or with an entry node that is not on loopback, `127.0.0.1:0` when every `-bootstrap` entry node is on loopback; `-listen` sets it | |

`kolo-node run -first` starts a new network with no other entry node: use it for the first node of your own network, on `127.0.0.1` for one computer or `0.0.0.0` for a network. `cmd/bootstrap` in this repository is a development entry node on `127.0.0.1:4000` that keeps its key in memory; the tests and the README do not need it.

`-bootstrap` (or `KOLO_CHAT_BOOTSTRAP`) takes one or more `host:port` entries, comma-separated, with an IPv4 address or a host name. The pins are a list: `-pin` is comma-separated and repeatable, `-pin-file` reads them from a file, and `KOLO_CHAT_PIN` holds a comma-separated list (see [pins](configuration.md#pins)). Connection relays are optional: `-relay host:port@key` (see [connection relays](configuration.md#connection-relays)).

To start over, quit the examples, stop the entry node and the pin with Ctrl-C, and delete `/tmp/kolo-chat-demo`.

## Same computer

Everything stays on loopback: nothing is reachable from another computer, and no firewall rule is needed. Four terminals, as the README shows: the entry node (`kolo-node run -first` on `127.0.0.1:7447`, with every example started with `-bootstrap 127.0.0.1:7447`), the pin (optional), Alice (the founder, with `KOLO_CHAT_CONVERSATION_KEY` set, and `KOLO_CHAT_PIN` if you run the pin), and Bob (without them). With no pin, three terminals are enough, and a message reaches someone who is offline only when its sender and that person are online together. Each person needs a data folder of their own; never run two examples on one. Once Alice runs, she can start more conversations with New conversation, with her pin or with none; Bob joins each with its own invite, through Join with an invite.

## Two computers on the same network

Computer A runs the entry node, the pin and Alice; computer B runs Bob.

Find A's LAN address, the `<ip-of-computer-a>` of the README:

- macOS: `ipconfig getifaddr en0` (Wi-Fi; `en1` or another interface for a cable on some Macs).
- Linux: `hostname -I`, the first IPv4 address.
- Windows: `ipconfig`, the "IPv4 Address" line.

Start both examples with `-bootstrap <ip-of-computer-a>:7447`. Because that address is not on loopback, the examples listen on `0.0.0.0:0` without a `-listen`. kolo-node logs `started` with `addr=0.0.0.0:7447`: that is the socket it listens on, not an address to give to B. Give B `<ip-of-computer-a>:7447`.

Open the ports on computer A:

- Linux with ufw: `sudo ufw allow 7447/udp` and `sudo ufw allow 7450/udp`.
- macOS with the firewall on: allow incoming connections for kolo-node, kolo-pin and Kolo Chat when macOS asks, or in System Settings, Network, Firewall, Options.
- Windows: allow kolo-node, kolo-pin and Kolo Chat when Windows asks, or add a rule for UDP 7447 and 7450 in Windows Defender Firewall.

The examples connect straight to each other, on a port the system picks. If a firewall blocks incoming UDP on A or B, give the example a fixed port, such as `-listen 0.0.0.0:7460`, and open it the same way.

Check from computer B:

```sh
kolo-node check <ip-of-computer-a>:7447
```

It prints `ok <ip-of-computer-a>:7447`, or `failed` with the reason. It proves that the entry node answers over UDP from B, which `ping` or a browser cannot.

## Two computers on different networks

Start both examples without `-bootstrap`: they use the public network, which needs nothing from you, and with no pin that is all. If you use a pin, it needs a public IPv4 address that B reaches, because there is no public pin and the invite carries its address. Two ways:

- Run `kolo-pin run` on a host with a public address, such as a VPS, with UDP 7450 open in its firewall and in the provider's. Alice then uses the host's address in `KOLO_CHAT_PIN`.
- Forward UDP port 7450 on A's router to A, and use the router's public address, which the router's admin page shows. This does not work when the provider puts A behind a shared address (carrier-grade NAT).

To keep a private network instead, also run `kolo-node run` on a host with a public address, with UDP 7447 open (kolo-node's [deploy guide](https://github.com/ciranda-dev/kolo-node/blob/main/docs/deploy.md) has a systemd unit and the firewall rules), and start every example with `-bootstrap <host>:7447`.

The entry node helps the two examples open a path through their routers, then they talk straight to each other. Some networks do not allow that, such as two carrier-grade NATs or a firewall that drops unknown UDP. A relay (`kolo-node relay`, UDP port 7448) carries the connection for such peers: start every example with the same `-relay host:port@key` (see [connection relays](configuration.md#connection-relays)). A pin, if you use one, still needs an address both computers reach.

## It does not connect

- Does the example say that no entry node answered? On the public network, check the internet connection and that UDP is allowed out; the public network has no uptime guarantee while Kolo is a beta. With `-bootstrap`, `kolo-node check <entry-node-address>` must print `ok` on every computer.
- Is UDP allowed? Ports 7447 (entry node of your own network) and 7450 (pin) are open on the computer or host that runs them, and in the provider's firewall for a VPS.
- Are both examples on the same network? Both without `-bootstrap` (public), or the same `-bootstrap` on every computer; a different one is another network, and the public network never meets your own.
- If you use a pin, is it reachable? The address in `KOLO_CHAT_PIN` is not `127.0.0.1` when the other person is on another computer, and the pin runs with `-listen 0.0.0.0:7450`. The invite carries the address the founder gave.
- Does the example listen beyond loopback? It does by default, except when every `-bootstrap` entry node is on loopback. With another computer, do not set `-listen` to a loopback address, and give the example a fixed port that is open if a firewall asks for one.
- Different networks: a pin, if you use one, needs a public address or a port forward (and so does your own entry node, if you run one). Behind networks that block a direct path, every example needs the same `-relay`; Settings shows whether each relay is ready.
- The window says why an invite fails; see [troubleshooting](troubleshooting.md).
