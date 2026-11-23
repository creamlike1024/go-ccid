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

// urb is struct usbdevfs_urb, without the frames of isochronous
// transfers. Go lays it out as C does: 56 bytes with 64-bit pointers, 44
// with 32-bit ones.
type urb struct {
	typ             uint8
	endpoint        uint8
	status          int32 // 0, or a negated errno
	flags           uint32
	buffer          unsafe.Pointer
	bufferLength    int32
	actualLength    int32
	startFrame      int32
	numberOfPackets int32
	errorCount      int32
	signr           uint32
	userContext     unsafe.Pointer
}

// ctrlTransfer is struct usbdevfs_ctrltransfer.
type ctrlTransfer struct {
	requestType, request uint8
	value, index, length uint16
	timeout              uint32 // in milliseconds
	data                 unsafe.Pointer
}

// urbTypeBulk is USBDEVFS_URB_TYPE_BULK.
const urbTypeBulk = 3

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
	reqControl          = ioc(iocRead|iocWrite, 'U', 0, unsafe.Sizeof(ctrlTransfer{}))
	reqSubmitURB        = ioc(iocRead, 'U', 10, unsafe.Sizeof(urb{}))
	reqDiscardURB       = ioc(iocNone, 'U', 11, 0)
	reqReapURB          = ioc(iocWrite, 'U', 12, unsafe.Sizeof(uintptr(0)))
	reqSetInterface     = ioc(iocRead, 'U', 4, unsafe.Sizeof(setInterface{}))
	reqClaimInterface   = ioc(iocRead, 'U', 15, unsafe.Sizeof(uint32(0)))
	reqReleaseInterface = ioc(iocRead, 'U', 16, unsafe.Sizeof(uint32(0)))
	reqReset            = ioc(iocNone, 'U', 20, 0)
	reqClearHalt        = ioc(iocRead, 'U', 21, unsafe.Sizeof(uint32(0)))
	reqDisconnectClaim  = ioc(iocRead, 'U', 27, unsafe.Sizeof(disconnectClaim{}))
)
