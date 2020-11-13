package main

import (
	"errors"
	"fmt"

	"github.com/OpenBazaar/wallet-interface"
	btcdhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btcdwire "github.com/btcsuite/btcd/wire"
	btc "github.com/btcsuite/btcutil"
	"github.com/btcsuite/btcutil/coinset"
	hd "github.com/btcsuite/btcutil/hdkeychain"
	"github.com/btcsuite/btcutil/txsort"
	"github.com/btcsuite/btcwallet/wallet/txauthor"
	"github.com/btcsuite/btcwallet/wallet/txrules"
	"github.com/ke-chain/btck/btcec"
	"github.com/ke-chain/btck/chaincfg"
	"github.com/ke-chain/btck/chaincfg/chainhash"
	"github.com/ke-chain/btck/db"
	"github.com/ke-chain/btck/peer"
	"github.com/ke-chain/btck/txscript"
	"github.com/ke-chain/btck/wire"
)

type Coin struct {
	TxHash       *btcdhash.Hash
	TxIndex      uint32
	TxValue      btc.Amount
	TxNumConfs   int64
	ScriptPubKey []byte
}

// Hash return has
func (c *Coin) Hash() *btcdhash.Hash { return c.TxHash }
func (c *Coin) Index() uint32        { return c.TxIndex }
func (c *Coin) Value() btc.Amount    { return c.TxValue }
func (c *Coin) PkScript() []byte     { return c.ScriptPubKey }
func (c *Coin) NumConfs() int64      { return c.TxNumConfs }
func (c *Coin) ValueAge() int64      { return int64(c.TxValue) * c.TxNumConfs }

func NewCoin(txid []byte, index uint32, value btc.Amount, numConfs int64, scriptPubKey []byte) coinset.Coin {
	shaTxid, _ := chainhash.NewHash(txid)
	bid := btcdhash.Hash(*shaTxid)
	c := &Coin{
		TxHash:       &bid,
		TxIndex:      index,
		TxValue:      value,
		TxNumConfs:   numConfs,
		ScriptPubKey: scriptPubKey,
	}
	return coinset.Coin(c)
}

func (w *SPVWallet) ScriptToAddress(script []byte) (btc.Address, error) {
	return scriptToAddress(script, w.params)
}

func scriptToAddress(script []byte, params *chaincfg.Params) (btc.Address, error) {
	_, addrs, _, err := txscript.ExtractPkScriptAddrs(script, params)
	if err != nil {
		return &btc.AddressPubKeyHash{}, err
	}
	if len(addrs) == 0 {
		return &btc.AddressPubKeyHash{}, errors.New("unknown script")
	}
	return addrs[0], nil
}

func (w *SPVWallet) gatherCoins() map[coinset.Coin]*hd.ExtendedKey {
	height, _ := w.blockchain.GetDB().Height()
	utxos, _ := w.txstore.Utxos().GetAll()
	m := make(map[coinset.Coin]*hd.ExtendedKey)
	for _, u := range utxos {
		if u.WatchOnly {
			continue
		}
		var confirmations int32
		if u.AtHeight > 0 {
			confirmations = int32(height) - u.AtHeight
		}
		c := NewCoin(u.Op.Hash.CloneBytes(), u.Op.Index, btc.Amount(db.String2Int64(u.Value)), int64(confirmations), u.ScriptPubkey)
		addr, err := w.ScriptToAddress(u.ScriptPubkey)
		if err != nil {
			continue
		}
		key, err := w.keyManager.GetKeyForScript(addr.ScriptAddress())
		if err != nil {
			continue
		}
		m[c] = key
	}
	return m
}

func (w *SPVWallet) GetFeePerByte(feeLevel wallet.FeeLevel) uint64 {
	return uint64(txrules.DefaultRelayFeePerKb)
}

func (w *SPVWallet) buildSpendAllTx(addr btc.Address, feeLevel wallet.FeeLevel) (*wire.MsgTx, error) {
	tx := wire.NewMsgTx(1)

	coinMap := w.gatherCoins()
	inVals := make(map[wire.OutPoint]int64)
	totalIn := int64(0)
	additionalPrevScripts := make(map[wire.OutPoint][]byte)
	additionalKeysByAddress := make(map[string]*btc.WIF)

	for coin, key := range coinMap {
		khash := chainhash.Hash(*coin.Hash())
		outpoint := wire.NewOutPoint(&khash, coin.Index())
		in := wire.NewTxIn(outpoint, nil, nil)
		additionalPrevScripts[*outpoint] = coin.PkScript()
		tx.TxIn = append(tx.TxIn, in)
		val := int64(coin.Value().ToUnit(btc.AmountSatoshi))
		totalIn += val
		inVals[*outpoint] = val

		addr, err := key.Address(chaincfg.GetbtcdPm(w.params))
		if err != nil {
			continue
		}
		privKey, err := key.ECPrivKey()
		if err != nil {
			continue
		}
		wif, _ := btc.NewWIF(privKey, chaincfg.GetbtcdPm(w.params), true)
		additionalKeysByAddress[addr.EncodeAddress()] = wif
	}

	// outputs
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return nil, err
	}

	// Get the fee
	feePerByte := int64(w.GetFeePerByte(feeLevel))
	estimatedSize := EstimateSerializeSize(1, []*wire.TxOut{wire.NewTxOut(0, script)}, false, P2PKH)
	fee := int64(estimatedSize) * feePerByte

	// Check for dust output
	if txrules.IsDustAmount(btc.Amount(totalIn-fee), len(script), txrules.DefaultRelayFeePerKb) {
		return nil, wallet.ErrorDustAmount
	}

	// Build the output
	out := wire.NewTxOut(totalIn-fee, script)
	tx.TxOut = append(tx.TxOut, out)

	// BIP 69 sorting
	txsort.InPlaceSort(wire.Convert2btcd(tx))

	// Sign
	getKey := txscript.KeyClosure(func(addr btc.Address) (*btcec.PrivateKey, bool, error) {
		addrStr := addr.EncodeAddress()
		wif, ok := additionalKeysByAddress[addrStr]
		if !ok {
			return nil, false, errors.New("key not found")
		}
		btcdkey := btcec.PrivateKey(*wif.PrivKey)
		return &btcdkey, wif.CompressPubKey, nil
	})
	getScript := txscript.ScriptClosure(func(
		addr btc.Address) ([]byte, error) {
		return []byte{}, nil
	})
	for i, txIn := range tx.TxIn {
		prevOutScript := additionalPrevScripts[txIn.PreviousOutPoint]
		script, err := txscript.SignTxOutput(w.params,
			tx, i, prevOutScript, txscript.SigHashAll, getKey,
			getScript, txIn.SignatureScript)
		if err != nil {
			return nil, errors.New("failed to sign transaction")
		}
		txIn.SignatureScript = script
	}
	return tx, nil
}

func NewUnsignedTransaction(outputs []*wire.TxOut, feePerKb btc.Amount, fetchInputs txauthor.InputSource, fetchChange txauthor.ChangeSource) (*txauthor.AuthoredTx, error) {

	var targetAmount btc.Amount
	for _, txOut := range outputs {
		targetAmount += btc.Amount(txOut.Value)
	}

	estimatedSize := EstimateSerializeSize(1, outputs, true, P2PKH)
	targetFee := txrules.FeeForSerializeSize(feePerKb, estimatedSize)

	for {
		inputAmount, inputs, _, scripts, err := fetchInputs(targetAmount + targetFee)
		if err != nil {
			return nil, err
		}
		if inputAmount < targetAmount+targetFee {
			return nil, errors.New("insufficient funds available to construct transaction")
		}

		maxSignedSize := EstimateSerializeSize(len(inputs), outputs, true, P2PKH)
		maxRequiredFee := txrules.FeeForSerializeSize(feePerKb, maxSignedSize)
		remainingAmount := inputAmount - targetAmount
		if remainingAmount < maxRequiredFee {
			targetFee = maxRequiredFee
			continue
		}

		// convert to btck msg
		ins := make([]*wire.TxIn, 0, 15)
		for _, in := range inputs {
			ins = append(ins, &wire.TxIn{
				PreviousOutPoint: wire.OutPoint{chainhash.Hash(in.PreviousOutPoint.Hash), in.PreviousOutPoint.Index},
				SignatureScript:  in.SignatureScript,
				Witness:          wire.TxWitness(in.Witness),
				Sequence:         in.Sequence,
			})
		}

		unsignedTransaction := &wire.MsgTx{
			Version:  wire.TxVersion,
			TxIn:     ins,
			TxOut:    outputs,
			LockTime: 0,
		}
		changeIndex := -1
		changeAmount := inputAmount - targetAmount - maxRequiredFee
		if changeAmount != 0 && !txrules.IsDustAmount(changeAmount,
			P2PKHOutputSize, txrules.DefaultRelayFeePerKb) {
			changeScript, err := fetchChange()
			if err != nil {
				return nil, err
			}
			if len(changeScript) > P2PKHPkScriptSize {
				return nil, errors.New("fee estimation requires change " +
					"scripts no larger than P2PKH output scripts")
			}
			change := wire.NewTxOut(int64(changeAmount), changeScript)
			l := len(outputs)
			unsignedTransaction.TxOut = append(outputs[:l:l], change)
			changeIndex = l
		}

		return &txauthor.AuthoredTx{
			Tx:          wire.Convert2btcd(unsignedTransaction),
			PrevScripts: scripts,
			TotalInput:  inputAmount,
			ChangeIndex: changeIndex,
		}, nil
	}
}

func (w *SPVWallet) buildTx(amount int64, addr btc.Address, feeLevel wallet.FeeLevel, optionalOutput *wire.TxOut) (*wire.MsgTx, error) {

	// Check for dust
	script, _ := txscript.PayToAddrScript(addr)
	if txrules.IsDustAmount(btc.Amount(amount), len(script), txrules.DefaultRelayFeePerKb) {
		return nil, wallet.ErrorDustAmount
	}

	var additionalPrevScripts map[btcdwire.OutPoint][]byte
	var additionalKeysByAddress map[string]*btc.WIF

	// Create input source
	coinMap := w.gatherCoins()
	coins := make([]coinset.Coin, 0, len(coinMap))
	for k := range coinMap {
		coins = append(coins, k)
	}

	inputSource := func(target btc.Amount) (total btc.Amount, inputs []*btcdwire.TxIn, inputValues []btc.Amount, scripts [][]byte, err error) {
		coinSelector := coinset.MaxValueAgeCoinSelector{MaxInputs: 10000, MinChangeAmount: btc.Amount(0)}
		coins, err := coinSelector.CoinSelect(target, coins)
		if err != nil {
			return total, inputs, []btc.Amount{}, scripts, wallet.ErrInsufficientFunds
		}
		additionalPrevScripts = make(map[btcdwire.OutPoint][]byte)
		additionalKeysByAddress = make(map[string]*btc.WIF)
		for _, c := range coins.Coins() {
			total += c.Value()
			khash := chainhash.Hash(*c.Hash())
			outpoint := btcdwire.NewOutPoint((*btcdhash.Hash)(&khash), c.Index())
			in := btcdwire.NewTxIn(outpoint, []byte{}, [][]byte{})
			in.Sequence = 0 // Opt-in RBF so we can bump fees
			inputs = append(inputs, in)
			additionalPrevScripts[*outpoint] = c.PkScript()
			key := coinMap[c]
			addr, err := key.Address(chaincfg.GetbtcdPm(w.params))
			if err != nil {
				continue
			}
			privKey, err := key.ECPrivKey()
			if err != nil {
				continue
			}
			wif, _ := btc.NewWIF(privKey, chaincfg.GetbtcdPm(w.params), true)
			additionalKeysByAddress[addr.EncodeAddress()] = wif
		}
		return total, inputs, []btc.Amount{}, scripts, nil
	}

	// Get the fee per kilobyte
	feePerKB := int64(w.GetFeePerByte(feeLevel)) * 1000

	// outputs
	out := wire.NewTxOut(amount, script)

	// Create change source
	changeSource := func() ([]byte, error) {
		addr := w.CurrentAddress(wallet.INTERNAL)
		script, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return []byte{}, err
		}
		return script, nil
	}

	outputs := []*wire.TxOut{out}
	if optionalOutput != nil {
		outputs = append(outputs, optionalOutput)
	}

	authoredTx, err := NewUnsignedTransaction(outputs, btc.Amount(feePerKB), inputSource, changeSource)
	if err != nil {
		return nil, err
	}

	// BIP 69 sorting
	txsort.InPlaceSort(authoredTx.Tx)

	// Sign tx
	getKey := txscript.KeyClosure(func(addr btc.Address) (*btcec.PrivateKey, bool, error) {
		addrStr := addr.EncodeAddress()
		wif := additionalKeysByAddress[addrStr]
		btcdkey := btcec.PrivateKey(*wif.PrivKey)
		return &btcdkey, wif.CompressPubKey, nil
	})
	getScript := txscript.ScriptClosure(func(
		addr btc.Address) ([]byte, error) {
		return []byte{}, nil
	})

	ktx := wire.Convert2btck(authoredTx.Tx)
	for i, txIn := range authoredTx.Tx.TxIn {
		prevOutScript := additionalPrevScripts[txIn.PreviousOutPoint]
		script, err := txscript.SignTxOutput(w.params,
			ktx, i, prevOutScript, txscript.SigHashAll, getKey,
			getScript, ktx.TxIn[i].SignatureScript)
		if err != nil {
			return nil, errors.New("Failed to sign transaction")
		}
		ktx.TxIn[i].SignatureScript = script
	}
	return ktx, nil
}

func (w *SPVWallet) Spend(amount int64, addr btc.Address, feeLevel wallet.FeeLevel, spendAll bool) (*chainhash.Hash, error) {
	var (
		tx  *wire.MsgTx
		err error
	)
	if spendAll {
		tx, err = w.buildSpendAllTx(addr, feeLevel)
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
	} else {
		tx, err = w.buildTx(amount, addr, feeLevel, nil)
		if err != nil {
			fmt.Println("buildTx:", err)
			return nil, err
		}
	}

	peer.WaitForAll.Add(1)
	w.Close()
	go btcdMain(tx)
	peer.WaitForAll.Wait()
	ch := tx.TxHash()
	return &ch, nil
}
