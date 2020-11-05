package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ke-chain/btck/blockchain"
	"github.com/ke-chain/btck/peer"
	"github.com/ke-chain/btck/wire"

	"github.com/ke-chain/btck/chaincfg"
	"github.com/ke-chain/btck/netsync"

	"github.com/ke-chain/btck/connmgr"
)

const (
	// defaultServices describes the default services that are supported by
	// the server.
	defaultServices = wire.SFNodeNetwork | wire.SFNodeBloom |
		wire.SFNodeWitness | wire.SFNodeCF

	// defaultTargetOutbound is the default number of outbound peers to target.
	defaultTargetOutbound = 8
)

var (
	// userAgentName is the user agent name and is used to help identify
	// ourselves to other bitcoin peers.
	userAgentName = "btcd"

	// userAgentVersion is the user agent version and is used to help
	// identify ourselves to other bitcoin peers.
	userAgentVersion = fmt.Sprintf("%d.%d.%d", appMajor, appMinor, appPatch)
)

// AddPeer adds a new peer that has already been connected to the server.
func (s *server) AddPeer(sp *serverPeer) {
	s.newPeers <- sp
}

// OnVerAck is invoked when a peer receives a verack bitcoin message and is used
// to kick start communication with them.
func (sp *serverPeer) OnVerAck(_ *peer.Peer, _ *wire.MsgVerAck) {
	sp.server.AddPeer(sp)
}

// OnInv is invoked when a peer receives an inv bitcoin message and is
// used to examine the inventory being advertised by the remote peer and react
// accordingly.  We pass the message down to blockmanager which will call
// QueueMessage with any appropriate responses.
func (sp *serverPeer) OnInv(_ *peer.Peer, msg *wire.MsgInv) {
	if !cfg.BlocksOnly {
		if len(msg.InvList) > 0 {
			sp.server.syncManager.QueueInv(msg, sp.Peer)
		}
		return
	}

	newInv := wire.NewMsgInvSizeHint(uint(len(msg.InvList)))
	for _, invVect := range msg.InvList {
		if invVect.Type == wire.InvTypeTx {
			peerLog.Tracef("Ignoring tx %v in inv from %v -- "+
				"blocksonly enabled", invVect.Hash, sp)
			if sp.ProtocolVersion() >= wire.BIP0037Version {
				peerLog.Infof("Peer %v is announcing "+
					"transactions -- disconnecting", sp)
				sp.Disconnect()
				return
			}
			continue
		}
		err := newInv.AddInvVect(invVect)
		if err != nil {
			peerLog.Errorf("Failed to add inventory vector: %v", err)
			break
		}
	}

	if len(newInv.InvList) > 0 {
		sp.server.syncManager.QueueInv(newInv, sp.Peer)
	}
}

// OnHeaders is invoked when a peer receives a headers bitcoin
// message.  The message is passed down to the sync manager.
func (sp *serverPeer) OnHeaders(_ *peer.Peer, msg *wire.MsgHeaders) {
	sp.server.syncManager.QueueHeaders(msg, sp.Peer)
}

// newPeerConfig returns the configuration for the given serverPeer.
func newPeerConfig(sp *serverPeer) *peer.Config {
	return &peer.Config{
		Listeners: peer.MessageListeners{
			OnInv:     sp.OnInv,
			OnVerAck:  sp.OnVerAck,
			OnHeaders: sp.OnHeaders,
		},
		UserAgentName:     userAgentName,
		UserAgentVersion:  userAgentVersion,
		UserAgentComments: cfg.UserAgentComments,
		ProtocolVersion:   peer.MaxProtocolVersion,
	}

}

// server provides a bitcoin server for handling communications to and from
// bitcoin peers.
type server struct {
	// The following variables must only be used atomically.
	// Putting the uint64s first makes them 64-bit aligned for 32-bit systems.
	bytesReceived uint64 // Total bytes received from all peers since start.
	bytesSent     uint64 // Total bytes sent by all peers since start.
	started       int32
	shutdown      int32
	startupTime   int64

	chainParams *chaincfg.Params
	connManager *connmgr.ConnManager
	chain       *blockchain.ChainSPV
	donePeers   chan *serverPeer
	newPeers    chan *serverPeer
	syncManager *netsync.SyncManager
	wg          sync.WaitGroup
	quit        chan struct{}
}

// serverPeer extends the peer to maintain state shared by the server and
// the blockmanager.
type serverPeer struct {
	*peer.Peer

	connReq       *connmgr.ConnReq
	server        *server
	persistent    bool
	isWhitelisted bool
	quit          chan struct{}
}

// onionAddr implements the net.Addr interface and represents a tor address.
type onionAddr struct {
	addr string
}

// peerState maintains state of inbound, persistent, outbound peers as well
// as banned peers and outbound groups.
type peerState struct {
	inboundPeers    map[int32]*serverPeer
	outboundPeers   map[int32]*serverPeer
	persistentPeers map[int32]*serverPeer
	banned          map[string]time.Time
	outboundGroups  map[string]int
}

// String returns the onion address.
//
// This is part of the net.Addr interface.
func (oa *onionAddr) String() string {
	return oa.addr
}

// Network returns "onion".
//
// This is part of the net.Addr interface.
func (oa *onionAddr) Network() string {
	return "onion"
}

// handleAddPeerMsg deals with adding new peers.  It is invoked from the
// peerHandler goroutine.
func (s *server) handleAddPeerMsg(state *peerState, sp *serverPeer) bool {

	// Signal the sync manager this peer is a new sync candidate.
	s.syncManager.NewPeer(sp.Peer)
	return true

}

// forAllOutboundPeers is a helper function that runs closure on all outbound
// peers known to peerState.
func (ps *peerState) forAllOutboundPeers(closure func(sp *serverPeer)) {
	for _, e := range ps.outboundPeers {
		closure(e)
	}
	for _, e := range ps.persistentPeers {
		closure(e)
	}
}

// forAllPeers is a helper function that runs closure on all peers known to
// peerState.
func (ps *peerState) forAllPeers(closure func(sp *serverPeer)) {
	for _, e := range ps.inboundPeers {
		closure(e)
	}
	ps.forAllOutboundPeers(closure)
}

// peerHandler is used to handle peer operations such as adding and removing
// peers to and from the server, banning peers, and broadcasting messages to
// peers.  It must be run in a goroutine.
func (s *server) peerHandler() {
	// Start the address manager and sync manager, both of which are needed
	// by peers.  This is done here since their lifecycle is closely tied
	// to this handler and rather than adding more channels to sychronize
	// things, it's easier and slightly faster to simply start and stop them
	// in this handler.
	s.syncManager.Start()

	go s.connManager.Start()

	state := &peerState{
		inboundPeers:    make(map[int32]*serverPeer),
		persistentPeers: make(map[int32]*serverPeer),
		outboundPeers:   make(map[int32]*serverPeer),
		banned:          make(map[string]time.Time),
		outboundGroups:  make(map[string]int),
	}

out:
	for {
		select {
		// New peers connected to the server.
		case p := <-s.newPeers:
			s.handleAddPeerMsg(state, p)

		case <-s.quit:
			// Disconnect all peers on server shutdown.
			state.forAllPeers(func(sp *serverPeer) {
				srvrLog.Tracef("Shutdown peer %s", sp)
				sp.Disconnect()
			})
			break out
		}
	}

	s.connManager.Stop()
	s.syncManager.Stop()

	// Drain channels before exiting so nothing is left waiting around
	// to send.
cleanup:
	for {
		select {
		case <-s.newPeers:
		case <-s.donePeers:
		default:
			break cleanup
		}
	}
	s.wg.Done()
	srvrLog.Tracef("Peer handler done")
}

// addrStringToNetAddr takes an address in the form of 'host:port' and returns
// a net.Addr which maps to the original address with any host names resolved
// to IP addresses.  It also handles tor addresses properly by returning a
// net.Addr that encapsulates the address.
func addrStringToNetAddr(addr string) (net.Addr, error) {
	host, strPort, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	port, err := strconv.Atoi(strPort)
	if err != nil {
		return nil, err
	}

	// Skip if host is already an IP address.
	if ip := net.ParseIP(host); ip != nil {
		return &net.TCPAddr{
			IP:   ip,
			Port: port,
		}, nil
	}

	// Tor addresses cannot be resolved to an IP, so just return an onion
	// address instead.
	if strings.HasSuffix(host, ".onion") {
		if cfg.NoOnion {
			return nil, errors.New("tor has been disabled")
		}

		return &onionAddr{addr: addr}, nil
	}

	// Attempt to look up an IP address associated with the parsed host.
	ips, err := btcdLookup(host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses found for %s", host)
	}

	return &net.TCPAddr{
		IP:   ips[0],
		Port: port,
	}, nil
}

// inboundPeerConnected is invoked by the connection manager when a new inbound
// connection is established.  It initializes a new inbound server peer
// instance, associates it with the connection, and starts a goroutine to wait
// for disconnection.
func (s *server) inboundPeerConnected(conn net.Conn) {
	sp := newServerPeer(s, false)
	sp.isWhitelisted = isWhitelisted(conn.RemoteAddr())
	sp.Peer = peer.NewInboundPeer(newPeerConfig(sp))
	sp.AssociateConnection(conn)
	go s.peerDoneHandler(sp)
}

// newServer returns a new btcd server configured to listen on addr for the
// bitcoin network type specified by chainParams.  Use start to begin accepting
// connections from peers.
func newServer() (*server, error) {
	s := server{
		newPeers: make(chan *serverPeer, cfg.MaxPeers),
	}
	// Create a new block chain instance with the appropriate configuration.
	var err error
	s.chain, err = blockchain.NewBlockchainSPV(cfg.DataDir, time.Now(), activeNetParams.Params)
	if err != nil {
		return nil, err
	}

	s.syncManager, err = netsync.New(&netsync.Config{
		Chain:       s.chain,
		ChainParams: s.chainParams,
		MaxPeers:    cfg.MaxPeers,
	})
	if err != nil {
		return nil, err
	}

	// Only setup a function to return new addresses to connect to when
	// not running in connect-only mode.  The simulation network is always
	// in connect-only mode since it is only intended to connect to
	// specified peers and actively avoid advertising and connecting to
	// discovered peers in order to prevent it from becoming a public test
	// network.
	var newAddressFunc func() (net.Addr, error)

	// Create a connection manager.
	// Create a connection manager.
	targetOutbound := defaultTargetOutbound
	cmgr, err := connmgr.New(&connmgr.Config{
		GetNewAddress:  newAddressFunc,
		TargetOutbound: uint32(targetOutbound),
		OnConnection:   s.outboundPeerConnected,
		Dial:           btcdDial,
		OnAccept:       s.inboundPeerConnected,
	})
	if err != nil {
		return nil, err
	}
	s.connManager = cmgr

	// Start up persistent peers.
	permanentPeers := cfg.ConnectPeers
	if len(permanentPeers) == 0 {
		permanentPeers = cfg.AddPeers
	}
	for _, addr := range permanentPeers {
		netAddr, err := addrStringToNetAddr(addr)
		if err != nil {
			return nil, err
		}

		go s.connManager.Connect(&connmgr.ConnReq{
			Addr:      netAddr,
			Permanent: true,
		})
	}

	return &s, nil

}

// newServerPeer returns a new serverPeer instance. The peer needs to be set by
// the caller.
func newServerPeer(s *server, isPersistent bool) *serverPeer {
	return &serverPeer{
		server:     s,
		persistent: isPersistent,
	}
}

// peerDoneHandler handles peer disconnects by notifiying the server that it's
// done along with other performing other desirable cleanup.
func (s *server) peerDoneHandler(sp *serverPeer) {
	sp.WaitForDisconnect()
	s.donePeers <- sp

	// Only tell sync manager we are gone if we ever told it we existed.
	if sp.VerAckReceived() {
		s.syncManager.DonePeer(sp.Peer)
	}
	close(sp.quit)
}

// outboundPeerConnected is invoked by the connection manager when a new
// outbound connection is established.  It initializes a new outbound server
// peer instance, associates it with the relevant state such as the connection
// request instance and the connection itself, and finally notifies the address
// manager of the attempt.
func (s *server) outboundPeerConnected(c *connmgr.ConnReq, conn net.Conn) {
	sp := newServerPeer(s, c.Permanent)
	p, err := peer.NewOutboundPeer(newPeerConfig(sp), c.Addr.String())
	if err != nil {
		srvrLog.Debugf("Cannot create outbound peer %s: %v", c.Addr, err)
		if c.Permanent {
			s.connManager.Disconnect(c.ID())
		} else {
			s.connManager.Remove(c.ID())
			go s.connManager.NewConnReq()
		}
		return
	}
	sp.Peer = p
	sp.connReq = c
	sp.AssociateConnection(conn)
	go s.peerDoneHandler(sp)
}

// WaitForShutdown blocks until the main listener and peer handlers are stopped.
func (s *server) WaitForShutdown() {
	s.wg.Wait()
}

// Start begins accepting connections from peers.
func (s *server) Start() {
	// Already started?
	if atomic.AddInt32(&s.started, 1) != 1 {
		return
	}
	srvrLog.Trace("Starting server")
	// Server startup time. Used for the uptime command for uptime calculation.
	s.startupTime = time.Now().Unix()
	// Start the peer handler which in turn starts the address and block
	// managers.
	s.wg.Add(1)
	go s.peerHandler()

}

// Stop gracefully shuts down the server by stopping and disconnecting all
// peers and the main listener.
func (s *server) Stop() error {
	// Make sure this only happens once.
	if atomic.AddInt32(&s.shutdown, 1) != 1 {
		srvrLog.Infof("Server is already in the process of shutting down")
		return nil
	}

	srvrLog.Warnf("Server shutting down")

	// Signal the remaining goroutines to quit.
	close(s.quit)
	return nil
}

// btcdLookup resolves the IP of the given host using the correct DNS lookup
// function depending on the configuration options.  For example, addresses will
// be resolved using tor when the --proxy flag was specified unless --noonion
// was also specified in which case the normal system DNS resolver will be used.
//
// Any attempt to resolve a tor address (.onion) will return an error since they
// are not intended to be resolved outside of the tor proxy.
func btcdLookup(host string) ([]net.IP, error) {
	if strings.HasSuffix(host, ".onion") {
		return nil, fmt.Errorf("attempt to resolve tor address %s", host)
	}

	return cfg.lookup(host)
}

// isWhitelisted returns whether the IP address is included in the whitelisted
// networks and IPs.
func isWhitelisted(addr net.Addr) bool {
	if len(cfg.whitelists) == 0 {
		return false
	}

	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		srvrLog.Warnf("Unable to SplitHostPort on '%s': %v", addr, err)
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		srvrLog.Warnf("Unable to parse IP '%s'", addr)
		return false
	}

	for _, ipnet := range cfg.whitelists {
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}
