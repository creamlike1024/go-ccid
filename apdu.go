package ccid

import "fmt"

// command is a parsed command APDU (ISO/IEC 7816-4, 5.1).
type command struct {
	header   [4]byte // CLA INS P1 P2
	data     []byte
	le       int // expected response length: -1 without Le; 256 or 65536 for Le 0
	extended bool
	cas      int // 1 to 4
}

func parseCommand(b []byte) (command, error) {
	var c command
	if len(b) < 4 {
		return c, fmt.Errorf("%w: %d bytes", ErrInvalidAPDU, len(b))
	}
	copy(c.header[:], b)
	body := b[4:]
	c.le = -1
	switch {
	case len(body) == 0:
		c.cas = 1
	case len(body) == 1:
		c.cas, c.le = 2, int(body[0])
		if c.le == 0 {
			c.le = 256
		}
	case body[0] != 0:
		lc := int(body[0])
		switch len(body) {
		case 1 + lc:
			c.cas = 3
		case 2 + lc:
			c.cas, c.le = 4, int(body[1+lc])
			if c.le == 0 {
				c.le = 256
			}
		default:
			return c, fmt.Errorf("%w: Lc %d with %d bytes after it", ErrInvalidAPDU, lc, len(body)-1)
		}
		c.data = body[1 : 1+lc]
	default: // extended: 00 followed by two bytes
		c.extended = true
		if len(body) < 3 {
			return c, fmt.Errorf("%w: extended length field of %d bytes", ErrInvalidAPDU, len(body))
		}
		n := int(body[1])<<8 | int(body[2])
		if len(body) == 3 {
			c.cas, c.le = 2, n
			if c.le == 0 {
				c.le = 65536
			}
			break
		}
		if n == 0 {
			return c, fmt.Errorf("%w: extended Lc 0", ErrInvalidAPDU)
		}
		switch len(body) {
		case 3 + n:
			c.cas = 3
		case 5 + n:
			c.cas = 4
			c.le = int(body[3+n])<<8 | int(body[4+n])
			if c.le == 0 {
				c.le = 65536
			}
		default:
			return c, fmt.Errorf("%w: extended Lc %d with %d bytes after it", ErrInvalidAPDU, n, len(body)-3)
		}
		c.data = body[3 : 3+n]
	}
	return c, nil
}

// getResponseClass returns the CLA of a GET RESPONSE for a command of
// class cla: the interindustry class with the same logical channel.
func getResponseClass(cla byte) byte {
	if cla&0xC0 == 0x40 { // further interindustry: channels 4 to 19
		return 0x40 | cla&0x0F
	}
	return cla & 0x03
}
