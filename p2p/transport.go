package p2p

// Peer is an interface that represents a remote node in the network.
type Peer interface {
}

// Transport is anything that handles the communication between the nodes in the network.
type Transport interface {
	ListenAndAccept() error
	Connect(address string) (Peer, error)
}
