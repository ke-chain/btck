package main

import (
	"log"
	"net"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/ke-chain/btck/peer"
)

var (
	cfg *config
)

// btcdMain is the real main function for btcd.  It is necessary to work around
// the fact that deferred functions do not run when os.Exit() is called.  The
// optional serverChan parameter is mainly used by the service code to be
// notified with the server once it is setup so it can gracefully stop it when
// requested from the service control manager.
func btcdMain() error {

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
	defer btcdLog.Info("Shutdown complete")
	btcdLog.Info("Snf")
	nodeURL := "127.0.0.1:9333"
	conn, err := net.Dial("tcp", nodeURL)
	if err != nil {
		log.Fatal(err)
	}

	// Create version message data.
	p, err := peer.NewOutboundPeerTemp(newPeerConfig(), nodeURL)
	if err != nil {
		srvrLog.Debugf("Cannot create outbound peer %s: %v", nodeURL, err)
		return err
	}
	p.AssociateConnection(conn)

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

	// Work around defer not working after os.Exit()
	if err := btcdMain(); err != nil {
		os.Exit(1)
	}
}
