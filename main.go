package main

import (
	"fmt"
	"net"

	"github.com/ke-chain/btck/peer"
	"github.com/ke-chain/btck/wire"
	"github.com/sirupsen/logrus"
)

func main() {
	nodeURL := "127.0.0.1:9333"
	// Create version message data.
	lastBlock := int32(234234)
	tcpAddrMe := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9334}
	me := wire.NewNetAddress(tcpAddrMe, wire.SFNodeNetwork)
	tcpAddrYou := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9333}
	you := wire.NewNetAddress(tcpAddrYou, wire.SFNodeNetwork)
	nonce, err := wire.RandomUint64()
	if err != nil {
		fmt.Printf("RandomUint64: error generating nonce: %v", err)
	}

	// Ensure we get the correct data back out.
	msg := wire.NewMsgVersion(me, you, nonce, lastBlock)
	msg.AddService(wire.SFNodeNetwork)
	conn, err := net.Dial("tcp", nodeURL)
	if err != nil {
		logrus.Fatalln(err)
	}
	defer conn.Close()

	p := peer.NewPeerTemp(conn)
	err = p.WriteMessage(msg, wire.LatestEncoding)

	if err != nil {
		logrus.Fatalln(err)
	}

	for {
		remoteMsg, _, err := p.ReadMessage(wire.LatestEncoding)
		switch remoteMsg.Command() {
		case wire.CmdVersion:
			logrus.Info(remoteMsg.Command(), msg.ProtocolVersion)

		case wire.CmdVerAck:
			logrus.Info(remoteMsg.Command())
			return
		}

		if err != nil {
			logrus.Error(err)
			return
		}

	}
}
