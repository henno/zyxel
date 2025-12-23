# zyxel - Zyxel Switch CLI Tool

A command-line tool for executing commands on Zyxel switches via SSH.

## Installation

```bash
go build -o zyxel .
```

## Configuration

Copy `.env.example` to `.env` and configure:

```bash
ZYXEL_HOST=192.168.1.1
ZYXEL_USER=admin
ZYXEL_PASSWORD=yourpassword
ZYXEL_PORT=22
```

## Usage

```bash
./zyxel -c 'show system-information'
./zyxel -c 'show running-config'
./zyxel -c 'show interface *'
./zyxel -c 'show mac address-table'
./zyxel -c 'show vlan'
./zyxel -c '?'
```
