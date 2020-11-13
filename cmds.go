package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/OpenBazaar/wallet-interface"
	"github.com/btcsuite/btcutil"
	"github.com/davecgh/go-spew/spew"
	"github.com/ke-chain/btck/chaincfg"
)

var spvwallet *SPVWallet

func newWalllet() *SPVWallet {

	spvwallet, _ = newSPVWallet()
	return spvwallet

}

func init() {
	activeNetParams = &simNetParams
}

// ServerCommand
type ServerCommand struct {
	Config
}

var serverCmd ServerCommand

func (x *ServerCommand) Execute(args []string) error {
	// Work around defer not working after os.Exit()
	if err := btcdMain(nil); err != nil {
		os.Exit(1)
	}
	return nil
}

// newAddressCommand
type newAddressCommand struct {
	Config
}

var newAddress newAddressCommand

func (x *newAddressCommand) Execute(args []string) error {

	addr := newWalllet().NewAddress(wallet.EXTERNAL)
	fmt.Println(addr.String())
	return nil
}

// CurrentAddress
type CurrentAddress struct{}

var currentAddress CurrentAddress

func (x *CurrentAddress) Execute(args []string) error {
	resp := newWalllet().CurrentAddress(wallet.EXTERNAL)
	fmt.Println(resp.String())
	return nil
}

// ListAddresses
type ListAddresses struct{}

var listAddresses ListAddresses

func (x *ListAddresses) Execute(args []string) error {

	addrs := newWalllet().ListAddresses()

	for _, addr := range addrs {
		fmt.Println(addr.String())
	}
	return nil
}

//AddWatchedAddress
type AddWatchedAddress struct {
	addr []string `short:"a long:"addr" description:"address"`
}

var addWatchedAddress AddWatchedAddress

func (x *AddWatchedAddress) Execute(args []string) error {

	addr, err := btcutil.DecodeAddress(args[0], chaincfg.GetbtcdPm(activeNetParams.Params))
	if err != nil {
		return err
	}
	newWalllet().AddWatchedAddresses(addr)
	return err
}

type Balance struct{}

var balance Balance

func (x *Balance) Execute(args []string) error {

	confirmed, unconfirmed := newWalllet().Balance()
	fmt.Println(btcutil.Amount(confirmed).ToBTC(), btcutil.Amount(unconfirmed).ToBTC())
	return nil
}

type Transactions struct{}

var transactions Transactions

func (x *Transactions) Execute(args []string) error {

	txs, _ := newWalllet().Transactions()
	spew.Dump(txs)
	return nil
}

type Spend struct {
}

var spend Spend

func (x *Spend) Execute(args []string) error {
	addr, _ := btcutil.DecodeAddress(args[0], chaincfg.GetbtcdPm(activeNetParams.Params))
	amout, _ := strconv.ParseInt(args[1], 10, 64)
	wa := newWalllet()
	satoshi, _ := btcutil.NewAmount(float64(amout))
	txhash, _ := wa.Spend(int64(satoshi), addr, wallet.NORMAL, false)
	fmt.Println(txhash)
	return nil
}
