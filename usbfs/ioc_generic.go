//go:build linux && !mips && !mipsle && !mips64 && !mips64le && !ppc64 && !ppc64le

package usbfs

// The ioctl encoding of most architectures (asm-generic/ioctl.h).
const (
	iocNone     = 0
	iocWrite    = 1
	iocRead     = 2
	iocSizeBits = 14
)
