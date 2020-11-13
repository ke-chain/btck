package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"

	"github.com/jessevdk/go-flags"
	"github.com/ke-chain/btck/wire"
)

var (
	cfg    *Config
	parser = flags.NewParser(nil, flags.Default)
)

// btcdMain is the real main function for btcd.  It is necessary to work around
// the fact that deferred functions do not run when os.Exit() is called.  The
// optional serverChan parameter is mainly used by the service code to be
// notified with the server once it is setup so it can gracefully stop it when
// requested from the service control manager.
func btcdMain(tx *wire.MsgTx) error {

	setLogLevels("TRC")
	// Load configuration and parse command line.  This function also
	// initializes logging and configures it accordingly.
	tcfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	cfg = tcfg
	defer func() {
		if logRotator != nil {
			logRotator.Close()
		}
	}()

	// Get a channel that will be closed when a shutdown signal has been
	// triggered either from an OS signal such as SIGINT (Ctrl+C) or from
	// another subsystem such as the RPC server.
	interrupt := interruptListener()

	// Create server and start it.
	server, err := newServer()
	if err != nil {
		// TODO: this logging could do with some beautifying.
		btcdLog.Errorf("Unable to start server on : %v", err)
		return err
	}
	defer func() {
		btcdLog.Infof("Gracefully shutting down the server...")
		server.Stop()
		server.WaitForShutdown()
		srvrLog.Infof("Server shutdown complete")
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for range c {
			fmt.Println("SPVWallet shutting down...")
			server.Stop()
			server.WaitForShutdown()
			srvrLog.Infof("Server shutdown complete")
			os.Exit(1)
		}
	}()

	server.Start()

	if tx != nil {
		server.syncManager.Broadcast(tx)
	}

	// Wait until the interrupt signal is received from an OS signal or
	// shutdown is requested through one of the subsystems such as the RPC
	// server.
	<-interrupt
	return nil
}

func main() {
	// Use all processor cores.
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Block and transaction processing can cause bursty allocations.  This
	// limits the garbage collector from excessively overallocating during
	// bursts.  This value was arrived at with the help of profiling live
	// usage.
	debug.SetGCPercent(10)

	parser.AddCommand("newaddress",
		"get a new bitcoin address",
		"Returns a new unused address in the keychain. Use caution when using this function as generating too many new addresses may cause the keychain to extend further than the wallet's lookahead window, meaning it might fail to recover all transactions when restoring from seed. CurrentAddress is safer as it never extends past the lookahead window.\n\n"+
			"Args:\n"+
			"1. purpose       (string default=external) The purpose for the address. Can be external for receiving from outside parties or internal for example, for change.\n\n"+
			"Examples:\n"+
			"> spvwallet newaddress\n"+
			"1DxGWC22a46VPEjq8YKoeVXSLzB7BA8sJS\n"+
			"> spvwallet newaddress internal\n"+
			"18zAxgfKx4NuTUGUEuB8p7FKgCYPM15DfS\n",
		&newAddress)
	parser.AddCommand("currentaddress",
		"get the current bitcoin address",
		"Returns the first unused address in the keychain\n\n"+
			"Args:\n"+
			"1. purpose       (string default=external) The purpose for the address. Can be external for receiving from outside parties or internal for example, for change.\n\n"+
			"Examples:\n"+
			"> spvwallet currentaddress\n"+
			"1DxGWC22a46VPEjq8YKoeVXSLzB7BA8sJS\n"+
			"> spvwallet currentaddress internal\n"+
			"18zAxgfKx4NuTUGUEuB8p7FKgCYPM15DfS\n",
		&currentAddress)
	parser.AddCommand("listaddresses",
		"list all addresses",
		"Returns all addresses currently watched by the wallet",
		&listAddresses)
	parser.AddCommand("addwatchedscript",
		"add a script to watch",
		"Add a script of bitcoin address to watch\n\n"+
			"Args:\n"+
			"1. script       (string) A hex encoded output script or bitcoin address.\n\n"+
			"Examples:\n"+
			"> spvwallet addwatchedscript 1DxGWC22a46VPEjq8YKoeVXSLzB7BA8sJS\n"+
			"> spvwallet addwatchedscript 76a914f318374559bf8296228e9c7480578a357081d59988ac\n",
		&addWatchedAddress)

	parser.AddCommand("balance",
		"get the wallet balance",
		"Returns both the confirmed and unconfirmed balances",
		&balance)
	parser.AddCommand("transactions",
		"get a list of transactions",
		"Returns a json list of the wallet's transactions",
		&transactions)

	parser.AddCommand("spend",
		"send bitcoins",
		"Send bitcoins to the given address\n\n"+
			"Args:\n"+
			"1. address       (string) The recipient's bitcoin address\n"+
			"2. amount        (integer) The amount to send in satoshi"+
			"Examples:\n"+
			"> spvwallet spend 1DxGWC22a46VPEjq8YKoeVXSLzB7BA8sJS 1000000\n"+
			"82bfd45f3564e0b5166ab9ca072200a237f78499576e9658b20b0ccd10ff325c",
		&spend)
	parser.AddCommand("server",
		"start the wallet",
		"The start command starts the wallet daemon",
		&serverCmd)

	if _, err := parser.Parse(); err != nil {
		os.Exit(1)
	}

}
