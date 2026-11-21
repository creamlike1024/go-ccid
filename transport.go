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
