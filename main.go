package main

import (
	"fmt"
	"log"

	"github.com/Souvik-223/rhino-framework/p2p"
)

func onPeer(Peer p2p.Peer) error {
	Peer.Close()
	return nil
}

func main() {
	tcpOps := p2p.TCPTransportOpts{
		ListenAddr:    ":3000",
		HandshakeFunc: p2p.NOPHandshakeFunc,
		Decoder:       p2p.DefaultDecoder{},
		onPeer:        onPeer,
	}
	tr := p2p.NewTCPTransport(tcpOps)

	go func() {
		for {
			msg := <-tr.Consume()
			fmt.Printf("%+v\n", msg)
		}
	}()

	if err := tr.ListenAndAccept(); err != nil {
		log.Fatal(err)
	}

	select {}
}
