# go-ccid

A pure Go host driver for USB smart card readers that speak CCID (the USB
Chip/Smart Card Interface Devices class), talking to the reader directly,
without pcscd, PC/SC or cgo.

It is meant for systems where PC/SC is not available, such as Android, where
an app gets the reader's file descriptor from `UsbManager` and hands it over.

**Status: under development, not usable yet.** The API will change.

## Scope

- `github.com/creamlike1024/go-ccid` (package `ccid`): the protocol,
  independent of how bytes reach the reader. CCID messages, time
  extensions, stale responses, the ATR, protocol and parameter selection,
  short and extended APDUs with chaining, T=0 at TPDU level (with optional
  GET RESPONSE and Le correction), and the reader's descriptors (exchange
  level, maximum message length, endpoints).
- `go-ccid/ccidtest`: a simulated reader and card, for tests of this
  package and of the programs that use it.
- Planned: T=1 at TPDU level, and `go-ccid/usbfs`, the transport for Linux
  and Android through usbdevfs on an open file descriptor, in pure Go.
  Other transports (macOS, Windows) may follow.

## Example

```go
r, err := ccid.Open(transport, iface.Descriptor, nil)
if err != nil {
	return err
}
defer r.Close()
card, err := r.Connect(ctx, 0, nil) // power on, ATR, protocol, parameters
if err != nil {
	return err
}
defer card.Disconnect(ctx)
resp, err := card.Transmit(ctx, []byte{0x00, 0xA4, 0x04, 0x00, 0x00})
```

`iface` comes from `ccid.FindInterfaces` on the device's raw descriptors.

## License

[MIT](LICENSE)
