// Package usbfs is a ccid.Transport for Linux and Android: it talks to a
// reader through usbfs, the kernel's interface for USB in user space, in
// pure Go, without libusb or cgo.
//
// On Linux, Open opens a device file, /dev/bus/usb/BBB/DDD, which
// Enumerate finds for the CCID readers attached; the user needs to be
// allowed to open it (a udev rule, or root). On Android, an app gets the
// same file, already open, from UsbManager.openDevice and
// UsbDeviceConnection.getFileDescriptor, and hands the descriptor to
// FromFD.
//
// A Device reads the device's descriptors from the file and finds its
// CCID interfaces; Claim claims one and returns its Endpoints, the
// Transport of a ccid.Reader:
//
//	dev, err := usbfs.Open("/dev/bus/usb/001/005", nil)
//	if err != nil {
//		return err
//	}
//	defer dev.Close()
//	ifs := dev.Interfaces()
//	ep, err := dev.Claim(ifs[0])
//	if err != nil {
//		return err
//	}
//	r, err := ccid.Open(ep, ifs[0].Descriptor, nil)
//
// An interface claimed by another program, such as pcscd, cannot be
// claimed: Claim then fails with ErrBusy.
//
// Each transfer is a URB submitted to the kernel, which keeps it until it
// completes: a response that comes late is not lost to a timeout, and a
// context that is done discards the transfer at once.
//
// The package builds on Linux and Android only.
package usbfs
