package ccid

import "context"

// Transport moves the bytes of CCID messages between the host and the
// reader: the bulk-OUT endpoint for commands and the bulk-IN endpoint for
// responses.
type Transport interface {
	// Write sends one complete command message.
	Write(ctx context.Context, p []byte) error
	// Read receives the next transfer from the bulk-IN endpoint into p and
	// returns its length. A response message may come in more than one
	// transfer; the Reader reads until it is complete. Read returns an
	// error when nothing arrives before the context's deadline or the
	// transport's own timeout.
	Read(ctx context.Context, p []byte) (int, error)
	// Close releases the transport.
	Close() error
}

// Controller is a Transport that also sends class-specific requests to
// the reader's interface on the default control pipe: the ABORT that,
// with PC_to_RDR_Abort, stops a command the host gave up on.
type Controller interface {
	Transport
	// ClassRequest sends a class-specific request with no data stage to
	// the reader's interface: bmRequestType 21h, wIndex the interface's
	// number.
	ClassRequest(ctx context.Context, request byte, value uint16) error
}
