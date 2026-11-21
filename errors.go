package ccid

import (
	"errors"
	"fmt"
)

var (
	// ErrNoCard: no card in the slot.
	ErrNoCard = errors.New("ccid: no card in the slot")
	// ErrCardInactive: the card is present but not powered.
	ErrCardInactive = errors.New("ccid: card not powered")
	// ErrCardMute: the card did not answer (ICC_MUTE).
	ErrCardMute = errors.New("ccid: card does not answer")
	// ErrNotSupported: the reader does not support the command, or this
	// package does not support what the reader or card needs.
	ErrNotSupported = errors.New("ccid: not supported")
	// ErrSlotBusy: the slot is busy with another command (CMD_SLOT_BUSY).
	ErrSlotBusy = errors.New("ccid: slot busy")
	// ErrAborted: the command was aborted (CMD_ABORTED).
	ErrAborted = errors.New("ccid: command aborted")
	// ErrProtocol: the reader answered in a way the CCID specification
	// does not allow.
	ErrProtocol = errors.New("ccid: protocol error")
	// ErrInvalidAPDU: the command APDU is malformed.
	ErrInvalidAPDU = errors.New("ccid: invalid command APDU")
	// ErrDisconnected: the card was disconnected.
	ErrDisconnected = errors.New("ccid: card disconnected")
)

// bError values of failed commands (CCID rev 1.1, table 6.2-2).
const (
	errCmdAborted            = 0xFF
	errICCMute               = 0xFE
	errXfrParity             = 0xFD
	errXfrOverrun            = 0xFC
	errHardware              = 0xFB
	errBadATRTS              = 0xF8
	errBadATRTCK             = 0xF7
	errProtocolNotSupported  = 0xF6
	errClassNotSupported     = 0xF5
	errProcedureByteConflict = 0xF4
	errDeactivatedProtocol   = 0xF3
	errBusyAutoSequence      = 0xF2
	errPINTimeout            = 0xF0
	errPINCancelled          = 0xEF
	errCmdSlotBusy           = 0xE0
	errCmdNotSupported       = 0x00
)

var errorNames = map[byte]string{
	errCmdAborted:            "CMD_ABORTED",
	errICCMute:               "ICC_MUTE",
	errXfrParity:             "XFR_PARITY_ERROR",
	errXfrOverrun:            "XFR_OVERRUN",
	errHardware:              "HW_ERROR",
	errBadATRTS:              "BAD_ATR_TS",
	errBadATRTCK:             "BAD_ATR_TCK",
	errProtocolNotSupported:  "ICC_PROTOCOL_NOT_SUPPORTED",
	errClassNotSupported:     "ICC_CLASS_NOT_SUPPORTED",
	errProcedureByteConflict: "PROCEDURE_BYTE_CONFLICT",
	errDeactivatedProtocol:   "DEACTIVATED_PROTOCOL",
	errBusyAutoSequence:      "BUSY_WITH_AUTO_SEQUENCE",
	errPINTimeout:            "PIN_TIMEOUT",
	errPINCancelled:          "PIN_CANCELLED",
	errCmdSlotBusy:           "CMD_SLOT_BUSY",
	errCmdNotSupported:       "command not supported",
}

// CommandError is a command the reader reports as failed.
type CommandError struct {
	Command MessageType
	ICC     ICCStatus
	// Code is bError: one of the codes of the CCID specification, or, for
	// a command with a wrong parameter, the offset of that parameter in
	// the command.
	Code byte
}

func (e *CommandError) Error() string {
	name, ok := errorNames[e.Code]
	if !ok {
		name = fmt.Sprintf("bad parameter at offset %d", e.Code)
	}
	return fmt.Sprintf("ccid: %v failed: %s (card %v)", e.Command, name, e.ICC)
}

// Is matches the package's errors: ErrNoCard, ErrCardInactive,
// ErrCardMute, ErrNotSupported, ErrSlotBusy and ErrAborted.
func (e *CommandError) Is(target error) bool {
	switch target {
	case ErrNoCard:
		return e.ICC == ICCAbsent
	case ErrCardInactive:
		return e.ICC == ICCInactive && e.Code != errICCMute
	case ErrCardMute:
		return e.Code == errICCMute && e.ICC != ICCAbsent
	case ErrNotSupported:
		return e.Code == errCmdNotSupported || e.Code == errProtocolNotSupported || e.Code == errClassNotSupported
	case ErrSlotBusy:
		return e.Code == errCmdSlotBusy || e.Code == errBusyAutoSequence
	case ErrAborted:
		return e.Code == errCmdAborted
	}
	return false
}
