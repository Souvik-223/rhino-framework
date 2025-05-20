package p2p

// Peer is an interface that represents a remote node in the network.
type Peer interface {
	close() error
}

// Transport is anything that handles the communication between the nodes in the network. This can of the form of TCP, UDP, WebSocket, etc.
type Transport interface {
	ListenAndAccept() error
	consume() <-chan RPC
}
