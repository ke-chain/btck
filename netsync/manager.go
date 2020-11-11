package netsync

import (
	"container/list"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcutil"
	peerpkg "github.com/ke-chain/btck/peer"

	"github.com/ke-chain/btck/chaincfg"
	"github.com/ke-chain/btck/wire"

	"github.com/ke-chain/btck/chaincfg/chainhash"

	"github.com/ke-chain/btck/blockchain"
)

const (
	// stallSampleInterval the interval at which we will check to see if our
	// sync has stalled.
	stallSampleInterval = 30 * time.Second

	// maxStallDuration is the time after which we will disconnect our
	// current sync peer if we haven't made progress.
	maxStallDuration = 3 * time.Minute
)

// zeroHash is the zero value hash (all zeros).  It is defined as a convenience.
var zeroHash chainhash.Hash

// SyncManager is used to communicate block related messages with peers. The
// SyncManager is started as by executing Start() in a goroutine. Once started,
// it selects peers to sync from and starts the initial block download. Once the
// chain is in sync, the SyncManager handles incoming block and header
// notifications and relays announcements of new blocks to peers.
type SyncManager struct {
	started     int32
	shutdown    int32
	chainParams *chaincfg.Params
	msgChan     chan interface{}
	wg          sync.WaitGroup
	quit        chan struct{}
	chain       *blockchain.ChainSPV

	// These fields should only be accessed from the blockHandler thread
	requestedBlocks  map[chainhash.Hash]struct{}
	syncPeer         *peerpkg.Peer
	lastProgressTime time.Time
	peerStates       map[*peerpkg.Peer]*peerSyncState

	// The following fields are used for headers-first mode.
	headersFirstMode bool
	headerList       *list.List
	startHeader      *list.Element
	nextCheckpoint   *chaincfg.Checkpoint

	walletCreationDate time.Time

	// for spv feature
	txStore *blockchain.TxStore
}

// peerSyncState stores additional information that the SyncManager tracks
// about a peer.
type peerSyncState struct {
	syncCandidate   bool
	requestQueue    []*wire.InvVect
	requestedTxns   map[chainhash.Hash]struct{}
	requestedBlocks map[chainhash.Hash]struct{}
	blockScore      int32
}

// newPeerMsg signifies a newly connected peer to the block handler.
type newPeerMsg struct {
	peer *peerpkg.Peer
}

// blockMsg packages a bitcoin block message and the peer it came from together
// so the block handler has access to that information.
type blockMsg struct {
	block *btcutil.Block
	peer  *peerpkg.Peer
	reply chan struct{}
}

// invMsg packages a bitcoin inv message and the peer it came from together
// so the block handler has access to that information.
type invMsg struct {
	inv  *wire.MsgInv
	peer *peerpkg.Peer
}

// merkleBlockMsg packages a merkle block message and the peer it came from
// together so the handler has access to that information.
type merkleBlockMsg struct {
	merkleBlock *wire.MsgMerkleBlock
	peer        *peerpkg.Peer
}

// headersMsg packages a bitcoin headers message and the peer it came from
// together so the block handler has access to that information.
type headersMsg struct {
	headers *wire.MsgHeaders
	peer    *peerpkg.Peer
}

// headerNode is used as a node in a list of headers that are linked together
// between checkpoints.
type headerNode struct {
	height int32
	hash   *chainhash.Hash
}

// donePeerMsg signifies a newly disconnected peer to the block handler.
type donePeerMsg struct {
	peer *peerpkg.Peer
}

type updateFiltersMsg struct{}

// isSyncCandidate returns whether or not the peer is a candidate to consider
// syncing from.
func (sm *SyncManager) isSyncCandidate(peer *peerpkg.Peer) bool {
	// Typically a peer is not a candidate for sync if it's not a full node,
	// however regression test is special in that the regression tool is
	// not a full node and still needs to be considered a sync candidate.

	// The peer is not a candidate for sync if it's not a full
	// node. Additionally, if the segwit soft-fork package has
	// activated, then the peer must also be upgraded.
	segwitActive := true

	nodeServices := peer.Services()
	if nodeServices&wire.SFNodeNetwork != wire.SFNodeNetwork ||
		(segwitActive && !peer.IsWitnessEnabled()) {
		return false
	}

	// Candidate if all checks passed.
	return true
}

// resetHeaderState sets the headers-first mode state to values appropriate for
// syncing from a new peer.
func (sm *SyncManager) resetHeaderState(newestHash *chainhash.Hash, newestHeight int32) {
	sm.headersFirstMode = false
	sm.headerList.Init()
	sm.startHeader = nil

	// When there is a next checkpoint, add an entry for the latest known
	// block into the header pool.  This allows the next downloaded header
	// to prove it links to the chain properly.
	if sm.nextCheckpoint != nil {
		node := headerNode{height: newestHeight, hash: newestHash}
		sm.headerList.PushBack(&node)
	}
}

// startSync will choose the best peer among the available candidate peers to
// download/sync the blockchain from.  When syncing is already running, it
// simply returns.  It also examines the candidates for any which are no longer
// candidates and removes them as needed.
func (sm *SyncManager) startSync() {
	// Return now if we're already syncing.
	if sm.syncPeer != nil {
		return
	}

	best, err := sm.chain.BestBlock()
	if err != nil {
		log.Error(err.Error())
		return
	}
	var higherPeers, equalPeers []*peerpkg.Peer
	for peer, state := range sm.peerStates {
		if !state.syncCandidate {
			continue
		}

		// Remove sync candidate peers that are no longer candidates due
		// to passing their latest known block.  NOTE: The < is
		// intentional as opposed to <=.  While technically the peer
		// doesn't have a later block when it's equal, it will likely
		// have one soon so it is a reasonable choice.  It also allows
		// the case where both are at 0 such as during regression test.
		if peer.LastBlock() < best.GetHeight() {
			state.syncCandidate = false
			continue
		}

		// If the peer is at the same height as us, we'll add it a set
		// of backup peers in case we do not find one with a higher
		// height. If we are synced up with all of our peers, all of
		// them will be in this set.
		if peer.LastBlock() == best.GetHeight() {
			equalPeers = append(equalPeers, peer)
			continue
		}

		// This peer has a height greater than our own, we'll consider
		// it in the set of better peers from which we'll randomly
		// select.
		higherPeers = append(higherPeers, peer)
	}

	// Pick randomly from the set of peers greater than our block height,
	// falling back to a random peer of the same height if none are greater.
	//
	// TODO(conner): Use a better algorithm to ranking peers based on
	// observed metrics and/or sync in parallel.
	var bestPeer *peerpkg.Peer
	switch {
	case len(higherPeers) > 0:
		bestPeer = higherPeers[rand.Intn(len(higherPeers))]

	case len(equalPeers) > 0:
		bestPeer = equalPeers[rand.Intn(len(equalPeers))]
	}

	// Start syncing from the best peer if one was selected.
	if bestPeer != nil {
		// Clear the requestedBlocks if the sync peer changes, otherwise
		// we may ignore blocks we need that the last sync peer failed
		// to send.
		sm.requestedBlocks = make(map[chainhash.Hash]struct{})

		locator := sm.chain.GetBlockLocator()

		log.Infof("Syncing to block height %d from peer %v",
			bestPeer.LastBlock(), bestPeer.Addr())

		sm.syncPeer = bestPeer

		bestPeer.PushGetHeadersMsg(locator, &zeroHash)

		// Reset the last progress time now that we have a non-nil
		// syncPeer to avoid instantly detecting it as stalled in the
		// event the progress time hasn't been updated recently.
		sm.lastProgressTime = time.Now()
	} else {
		log.Warnf("No sync peer candidates available")
	}
}

// QueueHeaders adds the passed headers message and peer to the block handling
// queue.
func (sm *SyncManager) QueueHeaders(headers *wire.MsgHeaders, peer *peerpkg.Peer) {
	// No channel handling here because peers do not need to block on
	// headers messages.
	if atomic.LoadInt32(&sm.shutdown) != 0 {
		return
	}

	sm.msgChan <- &headersMsg{headers: headers, peer: peer}
}

// QueueInv adds the passed inv message and peer to the block handling queue.
func (sm *SyncManager) QueueInv(inv *wire.MsgInv, peer *peerpkg.Peer) {
	// No channel handling here because peers do not need to block on inv
	// messages.
	if atomic.LoadInt32(&sm.shutdown) != 0 {
		return
	}

	sm.msgChan <- &invMsg{inv: inv, peer: peer}
}

func (sm *SyncManager) updateFilterAndSend(peer *peerpkg.Peer) {
	if sm.txStore != nil {
		filter, err := sm.txStore.GimmeFilter()
		if err == nil {
			msgfl := filter.MsgFilterLoad()
			peer.QueueMessage(wire.NewMsgFilterLoad(msgfl.Filter, msgfl.HashFuncs, msgfl.Tweak, wire.BloomUpdateType(msgfl.Flags)), nil)
		} else {
			log.Errorf("Error loading bloom filter: %s", err.Error())
		}
	}
}

// handleNewPeerMsg deals with new peers that have signalled they may
// be considered as a sync peer (they have already successfully negotiated).  It
// also starts syncing if needed.  It is invoked from the syncHandler goroutine.
func (sm *SyncManager) handleNewPeerMsg(peer *peerpkg.Peer) {
	// Ignore if in the process of shutting down.
	if atomic.LoadInt32(&sm.shutdown) != 0 {
		return
	}

	log.Infof("New valid peer %s (%s)", peer, peer.UserAgent())

	// Initialize the peer state
	isSyncCandidate := sm.isSyncCandidate(peer)
	sm.peerStates[peer] = &peerSyncState{
		syncCandidate:   isSyncCandidate,
		requestedTxns:   make(map[chainhash.Hash]struct{}),
		requestedBlocks: make(map[chainhash.Hash]struct{}),
	}

	sm.updateFilterAndSend(peer)

	// Start syncing by choosing the best candidate if needed.
	if isSyncCandidate && sm.syncPeer == nil {
		sm.startSync()
	}
}

// clearRequestedState wipes all expected transactions and blocks from the sync
// manager's requested maps that were requested under a peer's sync state, This
// allows them to be rerequested by a subsequent sync peer.
func (sm *SyncManager) clearRequestedState(state *peerSyncState) {

	// Remove requested blocks from the global map so that they will be
	// fetched from elsewhere next time we get an inv.
	// TODO: we could possibly here check which peers have these blocks
	// and request them now to speed things up a little.
	for blockHash := range state.requestedBlocks {
		delete(sm.requestedBlocks, blockHash)
	}
}

// shouldDCStalledSyncPeer determines whether or not we should disconnect a
// stalled sync peer. If the peer has stalled and its reported height is greater
// than our own best height, we will disconnect it. Otherwise, we will keep the
// peer connected in case we are already at tip.
func (sm *SyncManager) shouldDCStalledSyncPeer() bool {
	lastBlock := sm.syncPeer.LastBlock()
	startHeight := sm.syncPeer.StartingHeight()

	var peerHeight int32
	if lastBlock > startHeight {
		peerHeight = lastBlock
	} else {
		peerHeight = startHeight
	}

	// If we've stalled out yet the sync peer reports having more blocks for
	// us we will disconnect them. This allows us at tip to not disconnect
	// peers when we are equal or they temporarily lag behind us.
	best, err := sm.chain.BestBlock()
	if err != nil {
		log.Error(err.Error())
		return false
	}
	return peerHeight > best.GetHeight()
}

// updateSyncPeer choose a new sync peer to replace the current one. If
// dcSyncPeer is true, this method will also disconnect the current sync peer.
// If we are in header first mode, any header state related to prefetching is
// also reset in preparation for the next sync peer.
func (sm *SyncManager) updateSyncPeer(dcSyncPeer bool) {
	log.Debugf("Updating sync peer, no progress for: %v",
		time.Since(sm.lastProgressTime))

	// First, disconnect the current sync peer if requested.
	if dcSyncPeer {
		sm.syncPeer.Disconnect()
	}

	// Reset any header state before we choose our next active sync peer.
	if sm.headersFirstMode {
		best, err := sm.chain.BestBlock()
		if err != nil {
			log.Error(err.Error())
			return
		}
		sm.resetHeaderState(best.BlockHash(), best.GetHeight())
	}

	sm.syncPeer = nil
	sm.startSync()
}

// handleStallSample will switch to a new sync peer if the current one has
// stalled. This is detected when by comparing the last progress timestamp with
// the current time, and disconnecting the peer if we stalled before reaching
// their highest advertised block.
func (sm *SyncManager) handleStallSample() {
	if atomic.LoadInt32(&sm.shutdown) != 0 {
		return
	}

	// If we don't have an active sync peer, exit early.
	if sm.syncPeer == nil {
		return
	}

	// If the stall timeout has not elapsed, exit early.
	if time.Since(sm.lastProgressTime) <= maxStallDuration {
		return
	}

	// Check to see that the peer's sync state exists.
	state, exists := sm.peerStates[sm.syncPeer]
	if !exists {
		return
	}

	sm.clearRequestedState(state)

	disconnectSyncPeer := sm.shouldDCStalledSyncPeer()
	sm.updateSyncPeer(disconnectSyncPeer)
}

// handleDonePeerMsg deals with peers that have signalled they are done.  It
// removes the peer as a candidate for syncing and in the case where it was
// the current sync peer, attempts to select a new best peer to sync from.  It
// is invoked from the syncHandler goroutine.
func (sm *SyncManager) handleDonePeerMsg(peer *peerpkg.Peer) {
	state, exists := sm.peerStates[peer]
	if !exists {
		log.Warnf("Received done peer message for unknown peer %s", peer)
		return
	}

	// Remove the peer from the list of candidate peers.
	delete(sm.peerStates, peer)

	log.Infof("Lost peer %s", peer)

	sm.clearRequestedState(state)

	if peer == sm.syncPeer {
		// Update the sync peer. The server has already disconnected the
		// peer before signaling to the sync manager.
		sm.updateSyncPeer(false)
	}
}

// handleHeadersMsg handles block header messages from all peers.  Headers are
// requested when performing a headers-first sync.
func (sm *SyncManager) handleHeadersMsg(hmsg *headersMsg) {
	peer := hmsg.peer
	if peer != sm.syncPeer {
		log.Warn("Received header message from a peer that isn't our sync peer")
		peer.Disconnect()
		return
	}
	_, exists := sm.peerStates[peer]
	if !exists {
		log.Warnf("Received headers message from unknown peer %s", peer)
		peer.Disconnect()
		return
	}

	msg := hmsg.headers
	numHeaders := len(msg.Headers)

	// Nothing to do for an empty headers message
	if numHeaders == 0 {
		return
	}

	// Process each header we received. Make sure when check that each one is before our
	// wallet creation date (minus the buffer). If we pass the creation date we will exit
	// request merkle blocks from this point forward and exit the function.
	badHeaders := 0
	for _, blockHeader := range msg.Headers {
		_, _, height, err := sm.chain.CommitHeader(*blockHeader)
		if err != nil {
			badHeaders++
			log.Errorf("Commit header error: %s", err.Error())
		}
		log.Infof("Received header %s at height %d", blockHeader.BlockHash().String(), height)

	}
	// Usually the peer will send the header at the tip of the chain in each batch. This will trigger
	// one commit error so we'll consider that acceptable, but anything more than that suggests misbehavior
	// so we'll dump this peer.
	if badHeaders > 1 {
		log.Warnf("Disconnecting from peer %s because he sent us too many bad headers", peer)
		peer.Disconnect()
		return
	}

	// Request the next batch of headers
	locator := sm.chain.GetBlockLocator()
	err := peer.PushGetHeadersMsg(locator, &zeroHash)
	if err != nil {
		log.Warnf("Failed to send getheaders message to peer %s: %v", peer.Addr(), err)
		return
	}
}

func (sm *SyncManager) Current() bool {
	best, err := sm.chain.BestBlock()
	if err != nil {
		return false
	}

	// If our best header's timestamp was more than 24 hours ago, we're probably not current
	if best.GetHeader().Timestamp.Before(time.Now().Add(-24 * time.Hour)) {
		return false
	}

	// Check our other peers to see if any are reporting a greater height than we have
	for peer := range sm.peerStates {
		if best.GetHeight() < peer.LastBlock() {
			return false
		}
	}
	return true
}

// handleMerkleBlockMsg handles merkle block messages from all peers.  Merkle blocks are
// requested in response to inv packets both during initial sync and after.
func (sm *SyncManager) handleMerkleBlockMsg(bmsg *merkleBlockMsg) {
	peer := bmsg.peer

	// We don't need to process blocks when we're syncing. They wont connect anyway
	if peer != sm.syncPeer && !sm.Current() {
		log.Warnf("Received block from %s when we aren't current", peer)
		return
	}
	state, exists := sm.peerStates[peer]
	if !exists {
		log.Warnf("Received merkle block message from unknown peer %s", peer)
		peer.Disconnect()
		return
	}

	// If we didn't ask for this block then the peer is misbehaving.
	merkleBlock := bmsg.merkleBlock
	header := merkleBlock.Header
	blockHash := header.BlockHash()
	if _, exists = state.requestedBlocks[blockHash]; !exists {
		// The regression test intentionally sends some blocks twice
		// to test duplicate block insertion fails.  Don't disconnect
		// the peer or ignore the block when we're in regression test
		// mode in this case so the chain code is actually fed the
		// duplicate blocks.
		if sm.chainParams.Name != chaincfg.RegressionNetParams.Name {
			log.Warnf("Got unrequested block %v from %s -- "+
				"disconnecting", blockHash, peer.Addr())
			peer.Disconnect()
			return
		}
	}

	// Remove block from request maps. Either chain will know about it and
	// so we shouldn't have any more instances of trying to fetch it, or we
	// will fail the insert and thus we'll retry next time we get an inv.
	delete(state.requestedBlocks, blockHash)
	delete(sm.requestedBlocks, blockHash)

	_, err := checkMBlock(merkleBlock)
	if err != nil {
		log.Warnf("Peer %s sent an invalid MerkleBlock", peer)
		peer.Disconnect()
		return
	}

	newBlock, _, newHeight, err := sm.chain.CommitHeader(header)
	// If this is an orphan block which doesn't connect to the chain, it's possible
	// that we might be synced on the longest chain, but not the most-work chain like
	// we should be. To make sure this isn't the case, let's sync from the peer who
	// sent us this orphan block.
	if err == blockchain.ErrOrphanHeader && sm.Current() {
		log.Debug("Received orphan header, checking peer for more blocks")
		state.requestQueue = []*wire.InvVect{}
		state.requestedBlocks = make(map[chainhash.Hash]struct{})
		sm.requestedBlocks = make(map[chainhash.Hash]struct{})
		sm.startSync()
		return
	} else if err == blockchain.ErrOrphanHeader && !sm.Current() {
		// The sync peer sent us an orphan header in the middle of a sync. This could
		// just be the last block in the batch which represents the tip of the chain.
		// In either case let's adjust the score for this peer downwards. If it goes
		// negative it means he's slamming us with blocks that don't fit in our chain
		// so disconnect.
		state.blockScore--
		if state.blockScore < 0 {
			log.Warnf("Disconnecting from peer %s because he sent us too many bad blocks", peer)
			peer.Disconnect()
			return
		}
		log.Warnf("Received unrequested block from peer %s", peer)
		return
	} else if err != nil {
		log.Error(err.Error())
		return
	}
	state.blockScore++

	if sm.Current() {
		peer.UpdateLastBlockHeight(int32(newHeight))
	}

	// We can exit here if the block is already known
	if !newBlock {
		log.Debugf("Received duplicate block %s", blockHash.String())
		return
	}

	log.Infof("Received merkle block %s at height %d", blockHash.String(), newHeight)

	// If we're not current and we've downloaded everything we've requested send another getblocks message.
	// Otherwise we'll request the next block in the queue.
	if !sm.Current() && len(state.requestQueue) == 0 {
		locator := sm.chain.GetBlockLocator()
		peer.PushGetBlocksMsg(locator, &zeroHash)
		log.Debug("Request queue at zero. Pushing new locator.")
	} else if !sm.Current() && len(state.requestQueue) > 0 {
		iv := state.requestQueue[0]
		iv.Type = wire.InvTypeFilteredBlock
		state.requestQueue = state.requestQueue[1:]
		state.requestedBlocks[iv.Hash] = struct{}{}
		gdmsg2 := wire.NewMsgGetData()
		gdmsg2.AddInvVect(iv)
		peer.QueueMessage(gdmsg2, nil)
		log.Debugf("Requesting block %s, len request queue: %d", iv.Hash.String(), len(state.requestQueue))
	}
}

// haveInventory returns whether or not the inventory represented by the passed
// inventory vector is known.  This includes checking all of the various places
// inventory can be when it is in different states such as blocks that are part
// of the main chain, on a side chain, in the orphan pool, and transactions that
// are in the memory pool (either the main pool or orphan pool).
func (sm *SyncManager) haveInventory(invVect *wire.InvVect) (bool, error) {
	switch invVect.Type {
	case wire.InvTypeWitnessBlock:
		fallthrough
	case wire.InvTypeBlock:
		// Ask chain if the block is known to it in any form (main
		// chain, side chain, or orphan).
		_, err := sm.chain.GetHeader(&invVect.Hash)
		if err != nil {
			return false, nil
		}
		return true, nil
	}
	// The requested inventory is is an unsupported type, so just claim
	// it is known to avoid requesting it.
	return true, nil
}

// handleInvMsg handles inv messages from all peers.
// We examine the inventory advertised by the remote peer and act accordingly.
func (sm *SyncManager) handleInvMsg(imsg *invMsg) {
	peer := imsg.peer
	state, exists := sm.peerStates[peer]
	if !exists {
		log.Warnf("Received inv message from unknown peer %s", peer)
		return
	}

	// Attempt to find the final block in the inventory list.  There may
	// not be one.
	lastBlock := -1
	invVects := imsg.inv.InvList
	for i := len(invVects) - 1; i >= 0; i-- {
		if invVects[i].Type == wire.InvTypeBlock {
			lastBlock = i
			break
		}
	}

	// If this inv contains a block announcement, and this isn't coming from
	// our current sync peer or we're current, then update the last
	// announced block for this peer. We'll use this information later to
	// update the heights of peers based on blocks we've accepted that they
	// previously announced.
	if lastBlock != -1 && (peer != sm.syncPeer || sm.Current()) {
		peer.UpdateLastAnnouncedBlock(&invVects[lastBlock].Hash)
	}

	// Ignore invs from peers that aren't the sync if we are not current.
	// Helps prevent fetching a mass of orphans.
	if peer != sm.syncPeer && !sm.Current() {
		return
	}

	// If our chain is current and a peer announces a block we already
	// know of, then update their current block height.
	if lastBlock != -1 && sm.Current() {
		sh, err := sm.chain.GetHeader(&invVects[lastBlock].Hash)
		if err == nil {
			peer.UpdateLastBlockHeight(sh.GetHeight())
		}
	}

	// Request the advertised inventory if we don't already have it
	gdmsg := wire.NewMsgGetData()
	shouldSendGetData := false
	if len(state.requestQueue) == 0 {
		shouldSendGetData = true
	}
	for _, iv := range invVects {

		// Add the inventory to the cache of known inventory
		// for the peer.
		peer.AddKnownInventory(iv)

		// Request the inventory if we don't already have it.
		haveInv, err := sm.haveInventory(iv)
		if err != nil {
			log.Warnf("Unexpected failure when checking for "+
				"existing inventory during inv message "+
				"processing: %v", err)
			continue
		}

		switch iv.Type {
		case wire.InvTypeFilteredBlock:
			fallthrough
		case wire.InvTypeBlock:
			// Block inventory goes into a request queue to be downloaded
			// one at a time. Sadly we can't batch these because the remote
			// peer  will not update the bloom filter until he's done processing
			// the batch which means we will have a super high false positive rate.
			if _, exists := sm.requestedBlocks[iv.Hash]; (!sm.Current() && !exists && !haveInv && shouldSendGetData) || sm.Current() {
				iv.Type = wire.InvTypeFilteredBlock
				state.requestQueue = append(state.requestQueue, iv)
			}
		default:
			continue
		}
	}

	// Pop the first block off the queue and request it
	if len(state.requestQueue) > 0 && (shouldSendGetData || sm.Current()) {
		iv := state.requestQueue[0]
		gdmsg.AddInvVect(iv)
		if len(state.requestQueue) > 1 {
			state.requestQueue = state.requestQueue[1:]
		} else {
			state.requestQueue = []*wire.InvVect{}
		}
		log.Debugf("Requesting block %s, len request queue: %d", iv.Hash.String(), len(state.requestQueue))
		state.requestedBlocks[iv.Hash] = struct{}{}
	}
	if len(gdmsg.InvList) > 0 {
		peer.QueueMessage(gdmsg, nil)
	}
}

// blockHandler is the main handler for the sync manager.  It must be run as a
// goroutine.  It processes block and inv messages in a separate goroutine
// from the peer handlers so the block (MsgBlock) messages are handled by a
// single thread without needing to lock memory data structures.  This is
// important because the sync manager controls which blocks are needed and how
// the fetching should proceed.
func (sm *SyncManager) blockHandler() {
	stallTicker := time.NewTicker(stallSampleInterval)
	defer stallTicker.Stop()

out:
	for {
		select {
		case m := <-sm.msgChan:
			switch msg := m.(type) {
			case *newPeerMsg:
				sm.handleNewPeerMsg(msg.peer)
			case *donePeerMsg:
				sm.handleDonePeerMsg(msg.peer)
			case *headersMsg:
				sm.handleHeadersMsg(msg)
			case *merkleBlockMsg:
				sm.handleMerkleBlockMsg(msg)
			case *invMsg:
				sm.handleInvMsg(msg)
			default:
				log.Warnf("Invalid message type in block "+
					"handler: %T", msg)
			}

		case <-stallTicker.C:
			sm.handleStallSample()

		case <-sm.quit:
			break out
		}
	}

	sm.wg.Done()
	log.Trace("Block handler done")

}

// Start begins the core block handler which processes block and inv messages.
func (sm *SyncManager) Start() {
	// Already started?
	if atomic.AddInt32(&sm.started, 1) != 1 {
		return
	}

	log.Trace("Starting sync manager")
	sm.wg.Add(1)
	go sm.blockHandler()
}

// New constructs a new SyncManager. Use Start to begin processing asynchronous
// block, tx, and inv updates.
func New(config *Config) (*SyncManager, error) {
	sm := SyncManager{
		chain:           config.Chain,
		chainParams:     config.ChainParams,
		requestedBlocks: make(map[chainhash.Hash]struct{}),
		peerStates:      make(map[*peerpkg.Peer]*peerSyncState),
		msgChan:         make(chan interface{}, config.MaxPeers*3),
		headerList:      list.New(),
		quit:            make(chan struct{}),
		txStore:         config.TxStore,
	}

	return &sm, nil
}

// NewPeer informs the sync manager of a newly active peer.
func (sm *SyncManager) NewPeer(peer *peerpkg.Peer) {
	// Ignore if we are shutting down.
	if atomic.LoadInt32(&sm.shutdown) != 0 {
		return
	}
	sm.msgChan <- &newPeerMsg{peer: peer}
}

// DonePeer informs the blockmanager that a peer has disconnected.
func (sm *SyncManager) DonePeer(peer *peerpkg.Peer) {
	// Ignore if we are shutting down.
	if atomic.LoadInt32(&sm.shutdown) != 0 {
		return
	}

	sm.msgChan <- &donePeerMsg{peer: peer}
}

// Stop gracefully shuts down the sync manager by stopping all asynchronous
// handlers and waiting for them to finish.
func (sm *SyncManager) Stop() error {
	if atomic.AddInt32(&sm.shutdown, 1) != 1 {
		log.Warnf("Sync manager is already in the process of " +
			"shutting down")
		return nil
	}

	log.Infof("Sync manager shutting down")
	close(sm.quit)
	sm.wg.Wait()
	return nil
}
