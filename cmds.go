package main

import (
	"fmt"
	"os"

	"github.com/OpenBazaar/wallet-interface"
)

type ServerCommand struct {
	All bool `short:"a" long:"all" description:"Add all files"`
}

var serverCmd ServerCommand

func (x *ServerCommand) Execute(args []string) error {
	// Work around defer not working after os.Exit()
	if err := btcdMain(); err != nil {
		os.Exit(1)
	}
	return nil
}

type newAddressCommand struct {
	Config
}

var newAddress newAddressCommand

func (x *newAddressCommand) Execute(args []string) error {
	// Work around defer not working after os.Exit()
	activeNetParams = &simNetParams
	spvwallet, err := newSPVWallet()
	if err != nil {
		fmt.Println(err)
	}

	addr := spvwallet.NewAddress(wallet.EXTERNAL)
	fmt.Println(addr.String())
	return nil
}

type CurrentAddress struct{}

var currentAddress CurrentAddress

func (x *CurrentAddress) Execute(args []string) error {
	// Work around defer not working after os.Exit()
	activeNetParams = &simNetParams
	spvwallet, err := newSPVWallet()
	if err != nil {
		fmt.Println(err)
	}

	resp := spvwallet.CurrentAddress(wallet.EXTERNAL)
	if err != nil {
		return err
	}
	fmt.Println(resp.String())
	return nil
}
