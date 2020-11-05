package netsync

import (
	"github.com/ke-chain/btck/blockchain"
	"github.com/ke-chain/btck/chaincfg"
)

// Config is a configuration struct used to initialize a new SyncManager.
type Config struct {
	ChainParams *chaincfg.Params
	Chain       *blockchain.ChainSPV
	MaxPeers    int
}
