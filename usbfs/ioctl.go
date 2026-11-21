//go:build linux

package usbfs

import "unsafe"

// ioc encodes an ioctl request as _IOC does.
func ioc(dir, typ, nr, size uintptr) uintptr {
	const (
		nrShift   = 0
		typeShift = 8
		sizeShift = 16
		dirShift  = sizeShift + iocSizeBits
	)
	return dir<<dirShift | typ<<typeShift | nr<<nrShift | size<<sizeShift
}

// bulkTransfer is struct usbdevfs_bulktransfer.
type bulkTransfer struct {
	ep, len, timeout uint32 // timeout in milliseconds, 0 for none
	data             unsafe.Pointer
}

// setInterface is struct usbdevfs_setinterface.
type setInterface struct {
	iface, altSetting uint32
}

// disconnectClaim is struct usbdevfs_disconnect_claim.
type disconnectClaim struct {
	iface, flags uint32
	driver       [256]byte
}

const disconnectClaimExceptDriver = 0x02

// The requests of linux/usbdevice_fs.h. The kernel names the direction
// of some of them backwards (_IOR for a value it reads); what counts is
// that the number is the same.
var (
	reqBulk             = ioc(iocRead|iocWrite, 'U', 2, unsafe.Sizeof(bulkTransfer{}))
	reqSetInterface     = ioc(iocRead, 'U', 4, unsafe.Sizeof(setInterface{}))
	reqClaimInterface   = ioc(iocRead, 'U', 15, unsafe.Sizeof(uint32(0)))
	reqReleaseInterface = ioc(iocRead, 'U', 16, unsafe.Sizeof(uint32(0)))
	reqReset            = ioc(iocNone, 'U', 20, 0)
	reqClearHalt        = ioc(iocRead, 'U', 21, unsafe.Sizeof(uint32(0)))
	reqDisconnectClaim  = ioc(iocRead, 'U', 27, unsafe.Sizeof(disconnectClaim{}))
)
