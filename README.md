# go-ccid

A pure Go host driver for USB smart card readers that speak CCID (the USB
Chip/Smart Card Interface Devices class), talking to the reader directly,
without pcscd, PC/SC or cgo.

It is meant for systems where PC/SC is not available, such as Android, where
an app gets the reader's file descriptor from `UsbManager` and hands it over.

**Status: early (v0.x).** The API may still change before v1.

```sh
go get github.com/creamlike1024/go-ccid
```

## Scope

- `github.com/creamlike1024/go-ccid` (package `ccid`): the protocol,
  independent of how bytes reach the reader. CCID messages, time
  extensions, stale responses, the ATR, protocol and parameter selection
  (with the PPS that TPDU level readers leave to the host),
  short and extended APDUs with chaining, T=0 at TPDU level (with optional
  GET RESPONSE and Le correction), T=1 at TPDU level (chaining, IFS, WTX,
  error recovery and resynchronization; LRC only), the reader's
  descriptors (exchange level, maximum message length, endpoints; ICCD
  devices of control transfers are told apart), and ABORT, which a
  command whose context ends sends by itself on a transport of control
  transfers.
- `go-ccid/usbfs`: the transport for Linux and Android, through the
  kernel's usbfs, in pure Go: open a device file, or take the file
  descriptor of Android's `UsbDeviceConnection`; list the readers attached
  (Linux, from sysfs). Transfers are URBs the kernel keeps until they
  complete, which a context discards.
- `go-ccid/ccidtest`: a simulated reader and card, for tests of this
  package and of the programs that use it, with the faults to test against:
  a card that takes its time, one removed, a reader unplugged, T=1 errors.
- Not supported: character level readers, ICCD A and B, T=1 with CRC,
  extended APDUs on T=0, the interrupt endpoint (card insertion and
  removal), PIN commands.
- Planned: other transports (macOS, Windows).

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
if err != nil {
	return err
}
if len(readers) == 0 {
	return errors.New("no CCID reader")
}
dev, err := usbfs.Open(readers[0].Path, nil)
if err != nil {
	return err
}
defer dev.Close()
iface := readers[0].Interfaces[0] // Enumerate lists devices with one at least
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

## ccid-probe

`cmd/ccid-probe` (Linux) lists the readers, connects to the card and sends
APDUs given in hex, with `-trace` printing every CCID message. `-reader`
opens a device file as it is, without listing (where sysfs can't be read):

```sh
go run github.com/creamlike1024/go-ccid/cmd/ccid-probe -trace 00A4040000
```

For example, reading an eUICC's EID, which changes nothing on the card:
open a logical channel, select the ISD-R on it, send GetEuiccData for
tag 5A, close the channel (here the card gave channel 1):

```sh
ccid-probe -getresponse 0070000001 \
  01A4040010A0000005591010FFFFFFFF890000010000 \
  81E2910006BF3E035C015A00 \
  0070800100
```

## License

[MIT](LICENSE)
