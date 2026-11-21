# go-ccid

A pure Go host driver for USB smart card readers that speak CCID (the USB
Chip/Smart Card Interface Devices class), talking to the reader directly,
without pcscd, PC/SC or cgo.

It is meant for systems where PC/SC is not available, such as Android, where
an app gets the reader's file descriptor from `UsbManager` and hands it over.

**Status: under development, not usable yet.** The API will change.

## Scope

- `ccid`: the protocol, independent of how bytes reach the reader. CCID
  messages, time extensions, T=0 at TPDU level (GET RESPONSE and Le
  correction), T=1 framing and chaining, short and extended APDUs, and the
  reader's class descriptor (exchange level, maximum message length).
- `ccid/usbfs`: the transport for Linux and Android, through usbdevfs on an
  open file descriptor, in pure Go.
- `ccid/ccidtest`: a simulated reader and card, for tests of this package
  and of the programs that use it.

Other transports (macOS, Windows) may follow.

## License

[MIT](LICENSE)
