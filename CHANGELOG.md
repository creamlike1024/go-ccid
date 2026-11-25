# Changelog

## v0.1.0

The first release.

- Package `ccid`: the CCID protocol over any transport.
  - CCID messages, time extensions, stale responses, ABORT when a
    command's context ends (on a transport of control transfers).
  - ATR parsing, protocol and parameter selection, and the PPS that TPDU
    level readers leave to the host.
  - Short and extended APDUs, with chaining, at the reader's exchange
    level: TPDU (T=0 with optional GET RESPONSE and Le correction; T=1
    with chaining, IFS, WTX, error recovery and resynchronization), short
    APDU and extended APDU.
  - The reader's class descriptor, and `FindInterfaces` for the CCID
    interfaces of a device's raw descriptors, composite devices included.
- Package `usbfs`: the transport for Linux and Android, in pure Go, of
  usbfs URBs; `Open` a device file or take Android's file descriptor with
  `FromFD`; `Enumerate` (Linux, from sysfs).
- Package `ccidtest`: a simulated reader and card, with faults to test
  against.
- `cmd/ccid-probe`: connects to a card and sends APDUs, optionally
  printing every CCID message.

Verified on real hardware: an ACS ACR38U-CCID (TPDU level) with a T=0
eUICC, on Android 10 and 16, reading and installing eSIM profiles.

Not supported: character level readers, ICCD A and B, T=1 with CRC,
extended APDUs on T=0, the interrupt endpoint, PIN commands.
