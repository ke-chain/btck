package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/OpenBazaar/wallet-interface"
	btc "github.com/btcsuite/btcutil"
	hd "github.com/btcsuite/btcutil/hdkeychain"
	"github.com/ke-chain/btck/blockchain"
	"github.com/ke-chain/btck/chaincfg"
	"github.com/ke-chain/btck/chaincfg/chainhash"
	"github.com/ke-chain/btck/db"
	"github.com/ke-chain/btck/txscript"
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

func newDefaultStore(sqliteDatastore *db.SQLiteDatastore) (*blockchain.TxStore, error) {

	mn, _ := sqliteDatastore.GetMnemonic()
	var mnemonic string
	if mn != "" {
		mnemonic = mn
	} else {
		//Create keyManager
		ent, err := b39.NewEntropy(128)
		if err != nil {
			return nil, err
		}
		mnemonic, err = b39.NewMnemonic(ent)
		if err != nil {
			return nil, err
		}
		if err := sqliteDatastore.SetMnemonic(mnemonic); err != nil {
			fmt.Println(err)
		}
		if err := sqliteDatastore.SetCreationDate(time.Now()); err != nil {
			fmt.Println(err)
		}
	}

	seed := b39.NewSeed(mnemonic, "")
	mPrivKey, err := hd.NewMaster(seed, chaincfg.GetbtcdPm(activeNetParams.Params))
	if err != nil {
		return nil, err
	}
	keyManager, err := blockchain.NewKeyManager(sqliteDatastore.Keys(), activeNetParams.Params, mPrivKey)

	return blockchain.NewTxStore(activeNetParams.Params, sqliteDatastore, keyManager)

}

func newSPVWallet() (*SPVWallet, error) {
	repoPath := ""
	// Select wallet datastore
	sqliteDatastore, _ := db.Create(repoPath)
	cd, _ := sqliteDatastore.GetCreationDate()
	store, _ := newDefaultStore(sqliteDatastore)

	datadir := filepath.Join(defaultDataDir, netName(activeNetParams))
	bc, _ := blockchain.NewBlockchainSPV(datadir, cd, activeNetParams.Params)
	w := &SPVWallet{
		txstore:      store,
		keyManager:   store.GetKeyManager(),
		params:       activeNetParams.Params,
		creationDate: cd,
		blockchain:   bc,
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

func (w *SPVWallet) ListAddresses() []btc.Address {
	keys := w.keyManager.GetKeys()
	addrs := []btc.Address{}
	for _, k := range keys {
		addr, err := k.Address(chaincfg.GetbtcdPm(activeNetParams.Params))
		if err != nil {
			continue
		}
		addrs = append(addrs, addr)
	}
	return addrs
}

func (w *SPVWallet) AddressToScript(addr btc.Address) ([]byte, error) {
	return txscript.PayToAddrScript(addr)
}

func (w *SPVWallet) AddWatchedAddresses(addrs ...btc.Address) error {

	var err error
	var watchedScripts [][]byte

	for _, addr := range addrs {
		script, err := w.AddressToScript(addr)
		if err != nil {
			return err
		}
		watchedScripts = append(watchedScripts, script)
	}

	err = w.txstore.WatchedScripts().PutAll(watchedScripts)
	w.txstore.PopulateAdrs()

	// w.wireService.MsgChan() <- updateFiltersMsg{}

	return err
}

func (w *SPVWallet) checkIfStxoIsConfirmed(utxo wallet.Utxo, stxos []wallet.Stxo) bool {
	for _, stxo := range stxos {
		if !stxo.Utxo.WatchOnly {
			if stxo.SpendTxid.IsEqual(&utxo.Op.Hash) {
				if stxo.SpendHeight > 0 {
					return true
				} else {
					return w.checkIfStxoIsConfirmed(stxo.Utxo, stxos)
				}
			} else if stxo.Utxo.IsEqual(&utxo) {
				if stxo.Utxo.AtHeight > 0 {
					return true
				} else {
					return false
				}
			}
		}
	}
	return false
}

func (w *SPVWallet) Balance() (confirmed, unconfirmed int64) {
	utxos, _ := w.txstore.Utxos().GetAll()
	stxos, _ := w.txstore.Stxos().GetAll()
	for _, utxo := range utxos {
		if !utxo.WatchOnly {
			if utxo.AtHeight > 0 {
				confirmed += db.String2Int64(utxo.Value)
			} else {
				if w.checkIfStxoIsConfirmed(utxo, stxos) {
					confirmed += db.String2Int64(utxo.Value)
				} else {
					unconfirmed += db.String2Int64(utxo.Value)
				}
			}
		}
	}
	return confirmed, unconfirmed
}

func (w *SPVWallet) ChainTip() (uint32, chainhash.Hash) {
	var ch chainhash.Hash
	sh, err := w.blockchain.GetDB().GetBestHeader()
	if err != nil {
		return 0, ch
	}
	return uint32(sh.GetHeight()), sh.GetHeader().BlockHash()
}

func (w *SPVWallet) Transactions() ([]wallet.Txn, error) {
	height, _ := w.ChainTip()
	txns, err := w.txstore.Txns().GetAll(false)
	if err != nil {
		return txns, err
	}
	for i, tx := range txns {
		var confirmations int32
		var status wallet.StatusCode
		confs := int32(height) - tx.Height + 1
		if tx.Height <= 0 {
			confs = tx.Height
		}
		switch {
		case confs < 0:
			status = wallet.StatusDead
		case confs == 0 && time.Since(tx.Timestamp) <= time.Hour*6:
			status = wallet.StatusUnconfirmed
		case confs == 0 && time.Since(tx.Timestamp) > time.Hour*6:
			status = wallet.StatusDead
		case confs > 0 && confs < 6:
			status = wallet.StatusPending
			confirmations = confs
		case confs > 5:
			status = wallet.StatusConfirmed
			confirmations = confs
		}
		tx.Confirmations = int64(confirmations)
		tx.Status = status
		txns[i] = tx
	}
	return txns, nil
}
