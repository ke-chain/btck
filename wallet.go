package main

import (
	"sync"
	"time"

	"github.com/OpenBazaar/wallet-interface"
	btc "github.com/btcsuite/btcutil"
	hd "github.com/btcsuite/btcutil/hdkeychain"
	"github.com/ke-chain/btck/blockchain"
	"github.com/ke-chain/btck/chaincfg"
	"github.com/ke-chain/btck/db"
	b39 "github.com/tyler-smith/go-bip39"
)

type SPVWallet struct {
	params *chaincfg.Params

	masterPrivateKey *hd.ExtendedKey
	masterPublicKey  *hd.ExtendedKey

	mnemonic string

	repoPath string

	blockchain *blockchain.ChainSPV
	txstore    *blockchain.TxStore
	keyManager *blockchain.KeyManager

	mutex *sync.RWMutex

	creationDate time.Time

	running bool
}

func newSPVWallet() (*SPVWallet, error) {

	// Select wallet datastore
	sqliteDatastore, _ := db.Create("")

	//Create keyManager
	ent, err := b39.NewEntropy(128)
	if err != nil {
		return nil, err
	}
	mnemonic, err := b39.NewMnemonic(ent)
	if err != nil {
		return nil, err
	}
	seed := b39.NewSeed(mnemonic, "")
	mPrivKey, err := hd.NewMaster(seed, chaincfg.GetbtcdPm(activeNetParams.Params))
	if err != nil {
		return nil, err
	}
	keyManager, err := blockchain.NewKeyManager(sqliteDatastore.Keys(), activeNetParams.Params, mPrivKey)

	var store *blockchain.TxStore
	store, err = blockchain.NewTxStore(activeNetParams.Params, sqliteDatastore, keyManager)

	w := &SPVWallet{
		txstore:    store,
		keyManager: keyManager,
		params:     activeNetParams.Params,
	}
	return w, nil
}

func (w *SPVWallet) CurrentAddress(purpose wallet.KeyPurpose) btc.Address {
	key, _ := w.keyManager.GetCurrentKey(purpose)
	addr, _ := key.Address(chaincfg.GetbtcdPm(activeNetParams.Params))
	return btc.Address(addr)
}

func (w *SPVWallet) NewAddress(purpose wallet.KeyPurpose) btc.Address {
	i, _ := w.txstore.Keys().GetUnused(purpose)
	key, _ := w.keyManager.GenerateChildKey(purpose, uint32(i[1]))
	addr, _ := key.Address(chaincfg.GetbtcdPm(activeNetParams.Params))
	w.txstore.Keys().MarkKeyAsUsed(addr.ScriptAddress())
	w.txstore.PopulateAdrs()
	return btc.Address(addr)
}

func (w *SPVWallet) Close() {
	if w.running {
		srvrLog.Info("Disconnecting from peers and shutting down")

		w.running = false
	}
}
