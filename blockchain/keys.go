package blockchain

import (
	"errors"

	"github.com/OpenBazaar/wallet-interface"
	hd "github.com/btcsuite/btcutil/hdkeychain"
	"github.com/ke-chain/btck/chaincfg"
)

type KeyManager struct {
	datastore wallet.Keys
	params    *chaincfg.Params

	internalKey *hd.ExtendedKey
	externalKey *hd.ExtendedKey
}

func (km *KeyManager) generateChildKey(purpose wallet.KeyPurpose, index uint32) (*hd.ExtendedKey, error) {
	if purpose == wallet.EXTERNAL {
		return km.externalKey.Child(index)
	} else if purpose == wallet.INTERNAL {
		return km.internalKey.Child(index)
	}
	return nil, errors.New("Unknown key purpose")
}

func (km *KeyManager) GetKeys() []*hd.ExtendedKey {
	var keys []*hd.ExtendedKey
	keyPaths, err := km.datastore.GetAll()
	if err != nil {
		return keys
	}
	for _, path := range keyPaths {
		k, err := km.generateChildKey(path.Purpose, uint32(path.Index))
		if err != nil {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}
