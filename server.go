package main

import (
	"fmt"

	"github.com/ke-chain/btck/peer"
	"github.com/ke-chain/btck/wire"
)

const (
	// defaultServices describes the default services that are supported by
	// the server.
	defaultServices = wire.SFNodeNetwork | wire.SFNodeBloom |
		wire.SFNodeWitness | wire.SFNodeCF
)

var (
	// userAgentName is the user agent name and is used to help identify
	// ourselves to other bitcoin peers.
	userAgentName = "btcd"

	// userAgentVersion is the user agent version and is used to help
	// identify ourselves to other bitcoin peers.
	userAgentVersion = fmt.Sprintf("%d.%d.%d", appMajor, appMinor, appPatch)
)

// newPeerConfig returns the configuration for the given serverPeer.
func newPeerConfig() *peer.Config {
	return &peer.Config{

		UserAgentName:     userAgentName,
		UserAgentVersion:  userAgentVersion,
		UserAgentComments: cfg.UserAgentComments,
		ProtocolVersion:   peer.MaxProtocolVersion,
	}

}
