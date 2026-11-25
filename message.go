package ccid

import (
	"encoding/binary"
	"fmt"
)

// HeaderLength is the length of a CCID message header.
const HeaderLength = 10

// MessageType is the bMessageType of a CCID message.
type MessageType byte

// Commands (host to reader).
const (
	PCtoRDRSetParameters   MessageType = 0x61
	PCtoRDRIccPowerOn      MessageType = 0x62
	PCtoRDRIccPowerOff     MessageType = 0x63
	PCtoRDRGetSlotStatus   MessageType = 0x65
	PCtoRDRSecure          MessageType = 0x69
	PCtoRDRT0APDU          MessageType = 0x6A
	PCtoRDREscape          MessageType = 0x6B
	PCtoRDRGetParameters   MessageType = 0x6C
	PCtoRDRResetParameters MessageType = 0x6D
	PCtoRDRIccClock        MessageType = 0x6E
	PCtoRDRXfrBlock        MessageType = 0x6F
	PCtoRDRMechanical      MessageType = 0x71
	PCtoRDRAbort           MessageType = 0x72
	PCtoRDRSetDataRate     MessageType = 0x73
)

// Responses (reader to host).
const (
	RDRtoPCDataBlock  MessageType = 0x80
	RDRtoPCSlotStatus MessageType = 0x81
	RDRtoPCParameters MessageType = 0x82
	RDRtoPCEscape     MessageType = 0x83
	RDRtoPCDataRate   MessageType = 0x84
)

var messageNames = map[MessageType]string{
	PCtoRDRSetParameters:   "PC_to_RDR_SetParameters",
	PCtoRDRIccPowerOn:      "PC_to_RDR_IccPowerOn",
	PCtoRDRIccPowerOff:     "PC_to_RDR_IccPowerOff",
	PCtoRDRGetSlotStatus:   "PC_to_RDR_GetSlotStatus",
	PCtoRDRSecure:          "PC_to_RDR_Secure",
	PCtoRDRT0APDU:          "PC_to_RDR_T0APDU",
	PCtoRDREscape:          "PC_to_RDR_Escape",
	PCtoRDRGetParameters:   "PC_to_RDR_GetParameters",
	PCtoRDRResetParameters: "PC_to_RDR_ResetParameters",
	PCtoRDRIccClock:        "PC_to_RDR_IccClock",
	PCtoRDRXfrBlock:        "PC_to_RDR_XfrBlock",
	PCtoRDRMechanical:      "PC_to_RDR_Mechanical",
	PCtoRDRAbort:           "PC_to_RDR_Abort",
	PCtoRDRSetDataRate:     "PC_to_RDR_SetDataRateAndClockFrequency",
	RDRtoPCDataBlock:       "RDR_to_PC_DataBlock",
	RDRtoPCSlotStatus:      "RDR_to_PC_SlotStatus",
	RDRtoPCParameters:      "RDR_to_PC_Parameters",
	RDRtoPCEscape:          "RDR_to_PC_Escape",
	RDRtoPCDataRate:        "RDR_to_PC_DataRateAndClockFrequency",
}

func (t MessageType) String() string {
	if s, ok := messageNames[t]; ok {
		return s
	}
	return fmt.Sprintf("MessageType(%#02x)", byte(t))
}

// Message is a CCID message: its header and its data (abData).
type Message struct {
	// Type is bMessageType.
	Type MessageType
	// Slot is bSlot, the slot the message is about.
	Slot byte
	// Seq is bSeq, which a response repeats from its command.
	Seq byte
	// Param holds the three message-specific bytes of the header: for
	// commands, as PowerOn's bPowerSelect or XfrBlock's bBWI and
	// wLevelParameter; for responses, bStatus, bError and the third byte
	// (bChainParameter, bClockStatus or bProtocolNum).
	Param [3]byte
	// Data is abData, of dwLength bytes.
	Data []byte
}

// MarshalBinary encodes the message, header and data.
func (m Message) MarshalBinary() ([]byte, error) {
	b := make([]byte, HeaderLength+len(m.Data))
	b[0] = byte(m.Type)
	binary.LittleEndian.PutUint32(b[1:], uint32(len(m.Data)))
	b[5], b[6] = m.Slot, m.Seq
	copy(b[7:10], m.Param[:])
	copy(b[10:], m.Data)
	return b, nil
}

// UnmarshalBinary decodes a complete message.
func (m *Message) UnmarshalBinary(b []byte) error {
	if len(b) < HeaderLength {
		return fmt.Errorf("ccid: message of %d bytes is shorter than its header", len(b))
	}
	n := binary.LittleEndian.Uint32(b[1:])
	if uint64(len(b)-HeaderLength) != uint64(n) {
		return fmt.Errorf("ccid: message says %d bytes of data, has %d", n, len(b)-HeaderLength)
	}
	m.Type = MessageType(b[0])
	m.Slot, m.Seq = b[5], b[6]
	copy(m.Param[:], b[7:10])
	m.Data = append([]byte(nil), b[10:]...)
	return nil
}

func (m Message) String() string {
	return fmt.Sprintf("%v slot %d seq %d param % x data(%d)", m.Type, m.Slot, m.Seq, m.Param[:], len(m.Data))
}

// ICCStatus is the state of the card in a slot (bmICCStatus).
type ICCStatus byte

const (
	ICCActive   ICCStatus = 0 // present and active
	ICCInactive ICCStatus = 1 // present and inactive
	ICCAbsent   ICCStatus = 2 // no card
)

func (s ICCStatus) String() string {
	switch s {
	case ICCActive:
		return "active"
	case ICCInactive:
		return "inactive"
	case ICCAbsent:
		return "absent"
	}
	return fmt.Sprintf("ICCStatus(%d)", byte(s))
}

// CommandStatus is the outcome of a command (bmCommandStatus).
type CommandStatus byte

// bmCommandStatus values.
const (
	CommandOK            CommandStatus = 0 // the command succeeded
	CommandFailed        CommandStatus = 1 // bError tells why
	CommandTimeExtension CommandStatus = 2 // the reader asks for more time
)

// ICCStatus returns the card status of a response.
func (m Message) ICCStatus() ICCStatus { return ICCStatus(m.Param[0] & 0x03) }

// CommandStatus returns the command status of a response.
func (m Message) CommandStatus() CommandStatus { return CommandStatus(m.Param[0] >> 6) }

// ErrorCode returns bError of a response.
func (m Message) ErrorCode() byte { return m.Param[1] }

// ChainParameter returns bChainParameter of a RDR_to_PC_DataBlock.
func (m Message) ChainParameter() byte { return m.Param[2] }

// Chaining values of XfrBlock's wLevelParameter and of DataBlock's
// bChainParameter, at the extended APDU level.
const (
	chainBeginEnd      = 0x00 // the APDU begins and ends in this block
	chainBeginMore     = 0x01 // it begins, and continues in the next
	chainContinueEnd   = 0x02 // it continues, and ends here
	chainContinueMore  = 0x03 // it continues, and continues in the next
	chainEmptyContinue = 0x10 // empty: the other side is to send the next block
)
