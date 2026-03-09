# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Home energy management system for a solar + battery (Powerwall) setup in New Zealand. Go service running on a NixOS host ("powerhouse"), publishing sensor data to Home Assistant via MQTT. Battery/inverter control logic lives in a separate repo (`powerctl`).

## Architecture

- **voltage-repeater/** — Reads Victron BLE devices (battery sense, solar chargers) and publishes readings to Home Assistant via MQTT. Uses TinyGo bluetooth library. Config: `voltage-repeater.ini` (MQTT credentials + device MACs/encryption keys).

## Build & Run

```bash
cd voltage-repeater && go build .
```

Dev shell (provides go + gopls): `nix-shell shell.nix` from any service directory.

Nix packaging: each service has `default.nix`/`derivation.nix` using `buildGoModule`.

## Deployment

`deploy.sh` rsyncs the repo to the `powerhouse` host and runs `nixos-rebuild switch`.

## Configuration

Uses `gookit/ini/v2`, loading config from `/etc/voltage-repeater.ini` first, then `./voltage-repeater.ini` as fallback. INI files contain credentials — they are not committed.

## Key Patterns

- MQTT entity auto-discovery: creates Home Assistant sensor entities via MQTT discovery protocol, then publishes state updates to topic-based state channels.
