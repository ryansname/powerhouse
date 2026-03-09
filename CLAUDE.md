# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Home energy management system for a solar + battery (Powerwall) setup in New Zealand. Two independent Go services that run on a NixOS host ("powerhouse"), publishing sensor data to Home Assistant via MQTT. Battery/inverter control logic has moved to a separate repo (`powerctl`).

## Architecture

**Two independent Go modules** (each has its own `go.mod`, no shared code):

- **wits-repeater/** — Fetches NZ wholesale electricity prices from WITS API (electricityinfo.co.nz) and publishes to Home Assistant via MQTT. Polls every minute. Config: `wits-repeater.ini` (MQTT credentials).

- **voltage-repeater/** — Reads Victron BLE devices (battery sense, solar chargers) and publishes readings to Home Assistant via MQTT. Uses TinyGo bluetooth library. Config: `voltage-repeater.ini` (MQTT credentials + device MACs/encryption keys).

## Build & Run

Each service is a standalone Go module. Build from within its directory:

```bash
cd wits-repeater && go build .
cd voltage-repeater && go build .
```

Dev shell (provides go + gopls): `nix-shell shell.nix` from any service directory.

Nix packaging: each service has `default.nix`/`derivation.nix` using `buildGoModule`.

## Deployment

`deploy.sh` rsyncs the repo to the `powerhouse` host and runs `nixos-rebuild switch`.

## Configuration

All services use `gookit/ini/v2` and load config from `/etc/<service>.ini` first, then `./<service>.ini` as fallback. INI files contain credentials — they are not committed (the checked-in ones are examples/development configs).

## Key Patterns

- MQTT entity auto-discovery: services create Home Assistant sensor entities via MQTT discovery protocol, then publish state updates to topic-based state channels.
