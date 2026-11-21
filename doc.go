// Package ccid is a host driver for USB smart card readers of the CCID
// class (USB Device Class: Smart Card, CCID rev 1.1), talking to the
// reader directly rather than through PC/SC.
//
// The package speaks the CCID protocol over a [Transport], which moves the
// bytes of CCID messages over the reader's bulk endpoints; package usbfs
// provides one for Linux and Android, and package ccidtest a simulated
// reader for tests.
//
// A [Reader] exchanges CCID messages with the reader: it numbers them,
// waits through the time extensions the reader asks for and drops stale
// responses of earlier commands. [Reader.Connect] powers a card on, reads
// its ATR, chooses the protocol (T=0 or T=1) and sets the reader's
// parameters, and returns a [Card], whose [Card.Transmit] sends a command
// APDU and returns the response APDU at the reader's exchange level:
//
//   - short or extended APDU level: the reader handles the protocol, and
//     long APDUs are sent and received in chained blocks;
//   - TPDU level, T=0: the APDU is mapped to a T=0 TPDU (the Le of a
//     case 4 command is left out, as ISO/IEC 7816-3 requires), and with
//     [ConnectOptions.AutoGetResponse] the GET RESPONSE and Le correction
//     of 61xx and 6Cxx are done here;
//   - character level is not supported.
//
// By default Transmit returns the card's status words as they are, as
// PC/SC's SCardTransmit does, so code written against PC/SC sees the same
// responses.
//
// A Reader serializes its exchanges: it may be used from several
// goroutines, one exchange at a time. After a context is cancelled in the
// middle of an exchange, the reader's late response is dropped by the
// next exchange, but the state of the card is unknown; reconnect.
package ccid
