# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- `-bootstrap`, `-relay` and `-relay-file` no longer stop at 16 entries, as kolo-swarm no longer does. Duplicate and clashing relays are still refused.
- `-max-pins` is 0 by default, no cap: the configuration and an invite may list any number of pins (an invite within kolo-invite's 1 MiB payload). A positive value still refuses more.
- An admin may keep any number of invites active, not 16, and the swarm no longer caps its topics at 32: every active invite is announced.

## [0.1.0] - 2026-11-20

### Added

- First release of kolo-chat-wails3-example.
