//go:build linux && (mips || mipsle || mips64 || mips64le || ppc64 || ppc64le)

package usbfs

// The ioctl encoding of MIPS and PowerPC.
const (
	iocNone     = 1
	iocRead     = 2
	iocWrite    = 4
	iocSizeBits = 13
)
