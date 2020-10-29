package wire

// ServiceFlag identifies services supported by a bitcoin peer.
type ServiceFlag uint64

// XXX pedro: we will probably need to bump this.
const (
	// ProtocolVersion is the latest protocol version this package supports.
	ProtocolVersion uint32 = 70013

	// MultipleAddressVersion is the protocol version which added multiple
	// addresses per message (pver >= MultipleAddressVersion).
	MultipleAddressVersion uint32 = 209

	// NetAddressTimeVersion is the protocol version which added the
	// timestamp field (pver >= NetAddressTimeVersion).
	NetAddressTimeVersion uint32 = 31402

	// BIP0031Version is the protocol version AFTER which a pong message
	// and nonce field in ping were added (pver > BIP0031Version).
	BIP0031Version uint32 = 60000

	// BIP0035Version is the protocol version which added the mempool
	// message (pver >= BIP0035Version).
	BIP0035Version uint32 = 60002

	// BIP0037Version is the protocol version which added new connection
	// bloom filtering related messages and extended the version message
	// with a relay flag (pver >= BIP0037Version).
	BIP0037Version uint32 = 70001

	// FeeFilterVersion is the protocol version which added a new
	// feefilter message.
	FeeFilterVersion uint32 = 70013
)

const (
	// SFNodeNetwork is a flag used to indicate a peer is a full node.
	SFNodeNetwork ServiceFlag = 1 << iota
	// SFNodeGetUTXO is a flag used to indicate a peer supports the
	// getutxos and utxos commands (BIP0064).
	SFNodeGetUTXO

	// SFNodeBloom is a flag used to indicate a peer supports bloom
	// filtering.
	SFNodeBloom

	// SFNodeWitness is a flag used to indicate a peer supports blocks
	// and transactions including witness data (BIP0144).
	SFNodeWitness

	// SFNodeXthin is a flag used to indicate a peer supports xthin blocks.
	SFNodeXthin

	// SFNodeBit5 is a flag used to indicate a peer supports a service
	// defined by bit 5.
	SFNodeBit5

	// SFNodeCF is a flag used to indicate a peer supports committed
	// filters (CFs).
	SFNodeCF

	// SFNode2X is a flag used to indicate a peer is running the Segwit2X
	// software.
	SFNode2X
)

const (

	// MainNet represents the main bitcoin network.
	MainNet BitcoinNet = 0xd9b4bef9

	// TestNet represents the regression test network.
	TestNet BitcoinNet = 0xdab5bffa

	// TestNet3 represents the test network (version 3).
	TestNet3 BitcoinNet = 0x0709110b

	// SimNet represents the simulation test network.
	SimNet BitcoinNet = 0x12141c16
)

// BitcoinNet represents which bitcoin network a message belongs to.
type BitcoinNet uint32
