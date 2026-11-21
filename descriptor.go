package ccid

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// DescriptorType is the type of the CCID class descriptor (CCID rev 1.1,
// section 5.1).
const DescriptorType = 0x21

// DescriptorLength is the length of the CCID class descriptor.
const DescriptorLength = 54

// InterfaceClass is the USB interface class of CCID readers.
const InterfaceClass = 0x0B

// ExchangeLevel is how much of the protocol with the card the reader
// handles itself (the exchange level bits of dwFeatures).
type ExchangeLevel uint32

const (
	// LevelCharacter: the host exchanges characters; not supported here.
	LevelCharacter ExchangeLevel = 0x00000000
	// LevelTPDU: the host sends TPDUs; T=0 procedure bytes and T=1
	// framing are the host's.
	LevelTPDU ExchangeLevel = 0x00010000
	// LevelShortAPDU: the reader exchanges short APDUs.
	LevelShortAPDU ExchangeLevel = 0x00020000
	// LevelExtendedAPDU: the reader exchanges short and extended APDUs.
	LevelExtendedAPDU ExchangeLevel = 0x00040000
)

const levelMask = 0x00070000

func (l ExchangeLevel) String() string {
	switch l {
	case LevelCharacter:
		return "character"
	case LevelTPDU:
		return "TPDU"
	case LevelShortAPDU:
		return "short APDU"
	case LevelExtendedAPDU:
		return "extended APDU"
	}
	return fmt.Sprintf("ExchangeLevel(%#x)", uint32(l))
}

// Features of dwFeatures besides the exchange level.
const (
	FeatureAutoParameters  = 0x00000002 // parameters configured from the ATR
	FeatureAutoActivation  = 0x00000004 // card activated on insertion
	FeatureAutoVoltage     = 0x00000008 // voltage selected by the reader
	FeatureAutoClock       = 0x00000010 // clock frequency changed by the reader
	FeatureAutoBaud        = 0x00000020 // baud rate changed by the reader
	FeatureAutoPPSProposal = 0x00000040 // PPS negotiated by the reader
	FeatureAutoPPSCurrent  = 0x00000080 // PPS made by the reader from its parameters
	FeatureClockStop       = 0x00000100
	FeatureNAD             = 0x00000200 // T=1 NAD other than 0 accepted
	FeatureAutoIFSD        = 0x00000400 // T=1 IFSD exchanged by the reader
	FeatureUSBWakeUp       = 0x00100000
)

// Voltages of bVoltageSupport and of PowerOn.
const (
	Voltage5V  = 0x01
	Voltage3V  = 0x02
	Voltage18V = 0x04
)

// Descriptor is the CCID class descriptor of a reader.
type Descriptor struct {
	CCIDVersion       uint16 // bcdCCID
	MaxSlotIndex      byte
	VoltageSupport    byte   // Voltage5V, Voltage3V, Voltage18V
	Protocols         uint32 // bit 0: T=0, bit 1: T=1
	DefaultClock      uint32 // kHz
	MaximumClock      uint32 // kHz
	NumClockSupported byte
	DataRate          uint32 // bps
	MaxDataRate       uint32 // bps
	NumDataRates      byte
	MaxIFSD           uint32
	SynchProtocols    uint32
	Mechanical        uint32
	Features          uint32
	MaxMessageLength  uint32 // dwMaxCCIDMessageLength
	ClassGetResponse  byte
	ClassEnvelope     byte
	LCDLayout         uint16
	PINSupport        byte
	MaxCCIDBusySlots  byte
}

// ParseDescriptor parses a CCID class descriptor: bLength, bDescriptorType
// 0x21 and the fields that follow.
func ParseDescriptor(b []byte) (Descriptor, error) {
	if len(b) < DescriptorLength {
		return Descriptor{}, fmt.Errorf("ccid: class descriptor of %d bytes, want %d", len(b), DescriptorLength)
	}
	if b[0] < DescriptorLength {
		return Descriptor{}, fmt.Errorf("ccid: class descriptor bLength %d, want %d", b[0], DescriptorLength)
	}
	if b[1] != DescriptorType {
		return Descriptor{}, fmt.Errorf("ccid: descriptor type %#02x, want %#02x", b[1], DescriptorType)
	}
	le16 := binary.LittleEndian.Uint16
	le32 := binary.LittleEndian.Uint32
	return Descriptor{
		CCIDVersion:       le16(b[2:]),
		MaxSlotIndex:      b[4],
		VoltageSupport:    b[5],
		Protocols:         le32(b[6:]),
		DefaultClock:      le32(b[10:]),
		MaximumClock:      le32(b[14:]),
		NumClockSupported: b[18],
		DataRate:          le32(b[19:]),
		MaxDataRate:       le32(b[23:]),
		NumDataRates:      b[27],
		MaxIFSD:           le32(b[28:]),
		SynchProtocols:    le32(b[32:]),
		Mechanical:        le32(b[36:]),
		Features:          le32(b[40:]),
		MaxMessageLength:  le32(b[44:]),
		ClassGetResponse:  b[48],
		ClassEnvelope:     b[49],
		LCDLayout:         le16(b[50:]),
		PINSupport:        b[52],
		MaxCCIDBusySlots:  b[53],
	}, nil
}

// MarshalBinary encodes the descriptor as the 54 bytes of a CCID class
// descriptor.
func (d Descriptor) MarshalBinary() ([]byte, error) {
	b := make([]byte, DescriptorLength)
	b[0], b[1] = DescriptorLength, DescriptorType
	le16 := binary.LittleEndian.PutUint16
	le32 := binary.LittleEndian.PutUint32
	le16(b[2:], d.CCIDVersion)
	b[4], b[5] = d.MaxSlotIndex, d.VoltageSupport
	le32(b[6:], d.Protocols)
	le32(b[10:], d.DefaultClock)
	le32(b[14:], d.MaximumClock)
	b[18] = d.NumClockSupported
	le32(b[19:], d.DataRate)
	le32(b[23:], d.MaxDataRate)
	b[27] = d.NumDataRates
	le32(b[28:], d.MaxIFSD)
	le32(b[32:], d.SynchProtocols)
	le32(b[36:], d.Mechanical)
	le32(b[40:], d.Features)
	le32(b[44:], d.MaxMessageLength)
	b[48], b[49] = d.ClassGetResponse, d.ClassEnvelope
	le16(b[50:], d.LCDLayout)
	b[52], b[53] = d.PINSupport, d.MaxCCIDBusySlots
	return b, nil
}

// ExchangeLevel returns the reader's exchange level.
func (d Descriptor) ExchangeLevel() ExchangeLevel {
	return ExchangeLevel(d.Features & levelMask)
}

// SupportsProtocol reports whether the reader supports T=0 (0) or T=1 (1).
func (d Descriptor) SupportsProtocol(p Protocol) bool {
	return p <= T1 && d.Protocols&(1<<p) != 0
}

// Interface is a CCID interface found in a configuration descriptor.
type Interface struct {
	Number     byte
	AltSetting byte
	Class      byte // InterfaceClass, or 0xFF for vendor readers with a CCID descriptor
	// Protocol is bInterfaceProtocol: 0 for CCID's bulk transfers (with
	// class 0x0B; vendor-defined with 0xFF).
	Protocol byte
	// Endpoint addresses (with the direction bit); InterruptIn is 0 when
	// the reader has no interrupt endpoint.
	BulkIn, BulkOut, InterruptIn byte
	// Maximum packet sizes of the bulk endpoints.
	BulkInMaxPacket, BulkOutMaxPacket uint16
	Descriptor                        Descriptor
}

// FindInterfaces returns the CCID interfaces of a device's raw
// descriptors: a configuration descriptor with its interfaces and
// endpoints, as Linux's usbfs and Android's
// UsbDeviceConnection.getRawDescriptors give them (a device descriptor
// before it is skipped). An interface counts as CCID when its class is
// 0x0B with protocol 0 (bulk transfers), or 0xFF with a CCID class
// descriptor, which some vendor readers have; the class descriptor may
// follow the endpoints, as some readers put it there.
//
// Class 0x0B with protocol 1 or 2 is a USB-ICC of the ICCD
// specification, versions A and B, which talk over control transfers
// rather than CCID's bulk endpoints. They are left out; a device with
// only those gets an error that is ErrNotSupported (ICCD devices of
// protocol 0 use bulk transfers and CCID messages, and count as CCID).
func FindInterfaces(raw []byte) ([]Interface, error) {
	var (
		found  []Interface
		cur    *Interface
		haveCD bool
		iccd   bool
	)
	finish := func() {
		switch {
		case cur == nil:
		case cur.Class == InterfaceClass && (cur.Protocol == ProtocolICCDA || cur.Protocol == ProtocolICCDB):
			iccd = true
		case haveCD && cur.BulkIn != 0 && cur.BulkOut != 0 &&
			(cur.Class == InterfaceClass && cur.Protocol == ProtocolBulk || cur.Class == 0xFF):
			found = append(found, *cur)
		}
		cur, haveCD = nil, false
	}
	for i := 0; i < len(raw); {
		n := int(raw[i])
		if n < 2 || i+n > len(raw) {
			return nil, fmt.Errorf("ccid: malformed descriptor at offset %d", i)
		}
		d := raw[i : i+n]
		switch d[1] {
		case 0x04: // interface
			finish()
			if n < 9 {
				return nil, fmt.Errorf("ccid: short interface descriptor at offset %d", i)
			}
			cur = &Interface{Number: d[2], AltSetting: d[3], Class: d[5], Protocol: d[7]}
		case 0x05: // endpoint
			if cur == nil || n < 7 {
				break
			}
			addr, attr := d[2], d[3]&0x03
			size := binary.LittleEndian.Uint16(d[4:]) & 0x07FF
			switch {
			case attr == 0x02 && addr&0x80 != 0:
				cur.BulkIn, cur.BulkInMaxPacket = addr, size
			case attr == 0x02:
				cur.BulkOut, cur.BulkOutMaxPacket = addr, size
			case attr == 0x03 && addr&0x80 != 0:
				cur.InterruptIn = addr
			}
		case DescriptorType:
			if cur == nil || haveCD {
				break
			}
			cd, err := ParseDescriptor(d)
			if err != nil {
				return nil, err
			}
			cur.Descriptor, haveCD = cd, true
		}
		i += n
	}
	finish()
	switch {
	case len(found) > 0:
		return found, nil
	case iccd:
		return nil, fmt.Errorf("%w: ICCD device of control transfers (version A or B)", ErrNotSupported)
	}
	return nil, ErrNoInterface
}

// bInterfaceProtocol values of class 0x0B.
const (
	ProtocolBulk  = 0x00 // CCID, and ICCD over bulk transfers
	ProtocolICCDA = 0x01 // ICCD version A: control transfers
	ProtocolICCDB = 0x02 // ICCD version B: control transfers, optional interrupt-IN
)

// ErrNoInterface is returned by FindInterfaces for a device with no CCID
// interface.
var ErrNoInterface = errors.New("ccid: no CCID interface")
