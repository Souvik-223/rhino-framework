package p2p

// handshakeFunc is a function that performs a handshake with the peer.
type HandshakeFunc func(any) error

func NOPHandshakeFunc(any) error { return nil }
