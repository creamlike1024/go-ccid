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
- `go-ccid/usbfs`: the transport for Linux and Android, through the
  kernel's usbfs, in pure Go: open a device file, or take the file
  descriptor of Android's `UsbDeviceConnection`; list the readers attached
  (Linux, from sysfs).
- `go-ccid/ccidtest`: a simulated reader and card, for tests of this
  package and of the programs that use it.
- Planned: T=1 at TPDU level. Other transports (macOS, Windows) may follow.

## Linux

Opening `/dev/bus/usb/BBB/DDD` needs permission on it, as a udev rule
gives (`ID_USB_INTERFACES` lists the device's interfaces as
`:class subclass protocol:`; this one matches CCID's `0b0000`):

```
# /etc/udev/rules.d/70-ccid.rules: CCID readers for the users of plugdev
SUBSYSTEM=="usb", ENV{DEVTYPE}=="usb_device", ENV{ID_USB_INTERFACES}=="*:0b0000:*", MODE="0660", GROUP="plugdev"
```

A reader that pcscd uses cannot be claimed (`usbfs.ErrBusy`): stop pcscd,
or use PC/SC for that reader.

## Example

```go
readers, err := usbfs.Enumerate() // Linux; on Android, usbfs.FromFD(fd, nil)
if err != nil || len(readers) == 0 {
	return err
}
dev, err := usbfs.Open(readers[0].Path, nil)
if err != nil {
	return err
}
defer dev.Close()
iface := dev.Interfaces()[0]
transport, err := dev.Claim(iface)
if err != nil {
	return err
}
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

Another transport gets its interface from `ccid.FindInterfaces` on the
device's raw descriptors.

## License

[MIT](LICENSE)
