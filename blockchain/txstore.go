package blockchain

import (
	"sync"

	"github.com/OpenBazaar/wallet-interface"
	btcdcfg "github.com/btcsuite/btcd/chaincfg"
	btcdwire "github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
	"github.com/btcsuite/btcutil/bloom"
	"github.com/ke-chain/btck/chaincfg"
)

type TxStore struct {
	adrs           []btcutil.Address
	watchedScripts [][]byte
	txids          map[string]int32
	txidsMutex     *sync.RWMutex
	addrMutex      *sync.Mutex
	cbMutex        *sync.Mutex

	keyManager *KeyManager

	params *chaincfg.Params

	listeners []func(wallet.TransactionCallback)

	wallet.Datastore
}

func (ts *TxStore) getbtcdParams() *btcdcfg.Params {
	return &btcdcfg.Params{
		PubKeyHashAddrID: ts.params.PubKeyHashAddrID,
	}
}

// PopulateAdrs just puts a bunch of adrs in ram; it doesn't touch the DB
func (ts *TxStore) PopulateAdrs() error {
	keys := ts.keyManager.GetKeys()
	ts.addrMutex.Lock()
	ts.adrs = []btcutil.Address{}
	for _, k := range keys {
		addr, err := k.Address(ts.getbtcdParams())
		if err != nil {
			continue
		}
		ts.adrs = append(ts.adrs, addr)
	}
	ts.addrMutex.Unlock()

	ts.watchedScripts, _ = ts.WatchedScripts().GetAll()
	txns, _ := ts.Txns().GetAll(true)
	ts.txidsMutex.Lock()
	for _, t := range txns {
		ts.txids[t.Txid] = t.Height
	}
	ts.txidsMutex.Unlock()

	return nil
}

// ... or I'm gonna fade away
func (ts *TxStore) GimmeFilter() (*bloom.Filter, error) {
	ts.PopulateAdrs()

	// get all utxos to add outpoints to filter
	allUtxos, err := ts.Utxos().GetAll()
	if err != nil {
		return nil, err
	}

	allStxos, err := ts.Stxos().GetAll()
	if err != nil {
		return nil, err
	}
	ts.addrMutex.Lock()
	elem := uint32(len(ts.adrs)+len(allUtxos)+len(allStxos)) + uint32(len(ts.watchedScripts))
	f := bloom.NewFilter(elem, 0, 0.00003, btcdwire.BloomUpdateAll)

	// note there could be false positives since we're just looking
	// for the 20 byte PKH without the opcodes.
	for _, a := range ts.adrs { // add 20-byte pubkeyhash
		f.Add(a.ScriptAddress())
	}
	ts.addrMutex.Unlock()
	for _, u := range allUtxos {
		f.AddOutPoint(&u.Op)
	}

	for _, s := range allStxos {
		f.AddOutPoint(&s.Utxo.Op)
	}

	return f, nil
}

func NewTxStore(p *chaincfg.Params, db wallet.Datastore, keyManager *KeyManager) (*TxStore, error) {
	txs := &TxStore{
		params:     p,
		keyManager: keyManager,
		addrMutex:  new(sync.Mutex),
		cbMutex:    new(sync.Mutex),
		txidsMutex: new(sync.RWMutex),
		txids:      make(map[string]int32),
		Datastore:  db,
	}
	err := txs.PopulateAdrs()
	if err != nil {
		return nil, err
	}
	return txs, nil
}
