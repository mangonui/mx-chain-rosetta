package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/multiversx/mx-chain-rosetta/server/factory"
	"github.com/multiversx/mx-chain-rosetta/version"
	"github.com/urfave/cli"
)

func main() {
	// This is a workaround, so that TCP sockets opened by rosetta (as a TCP client) to read data from the observer can be reused more easily,
	// once their TCP TIME_WAIT expires.
	// The default value would have been very small (e.g. 2), thus forcing the TCP client (rosetta)
	// to << read, CLOSING, TIME_WAIT, CLOSED >>, without any reuse - thus easily exhausting all available sockets in a short amount of time,
	// under heavy load.
	// References: https://github.com/golang/go/issues/16012
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 512

	app := cli.NewApp()
	cli.AppHelpTemplate = helpTemplate
	app.Name = "MultiversX Rosetta CLI App"
	app.Version = version.RosettaMiddlewareVersion
	app.Usage = "This is the entry point for starting a new MultiversX Rosetta instance"
	app.Flags = getAllCliFlags()
	app.Authors = []cli.Author{
		{
			Name:  "The MultiversX Team",
			Email: "contact@multiversx.com",
		},
	}

	app.Action = startRosetta

	err := app.Run(os.Args)
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func startRosetta(ctx *cli.Context) error {
	cliFlags := getParsedCliFlags(ctx)

	fileLogging, err := initializeLogger(cliFlags.logsFolder, cliFlags.logLevel)
	if err != nil {
		return err
	}

	customCurrencies, err := decideCustomCurrencies(cliFlags.configFileCustomCurrencies)
	if err != nil {
		return err
	}

	log.Info("Starting Rosetta...", "middleware", version.RosettaMiddlewareVersion, "specification", version.RosettaVersion)

	networkProvider, err := factory.CreateNetworkProvider(factory.ArgsCreateNetworkProvider{
		IsOffline:                   cliFlags.offline,
		NumShards:                   cliFlags.numShards,
		ObservedActualShard:         cliFlags.observerActualShard,
		ObservedProjectedShard:      cliFlags.observerProjectedShard,
		ObservedProjectedShardIsSet: cliFlags.observerProjectedShardIsSet,
		ObserverUrl:                 cliFlags.observerHttpUrl,
		BlockchainName:              cliFlags.blockchainName,
		NetworkID:                   cliFlags.networkID,
		NetworkName:                 cliFlags.networkName,
		GasPerDataByte:              cliFlags.gasPerDataByte,
		GasPriceModifier:            cliFlags.gasPriceModifier,
		GasLimitCustomTransfer:      cliFlags.gasLimitCustomTransfer,
		MinGasPrice:                 cliFlags.minGasPrice,
		MinGasLimit:                 cliFlags.minGasLimit,
		ExtraGasLimitGuardedTx:      cliFlags.extraGasLimitGuardedTx,
		ExtraGasLimitRelayedTxV3:    cliFlags.extraGasLimitRelayedTxV3,
		NativeCurrencySymbol:        cliFlags.nativeCurrencySymbol,
		CustomCurrencies:            customCurrencies,
		GenesisBlockHash:            cliFlags.genesisBlock,
		FirstHistoricalEpoch:        cliFlags.firstHistoricalEpoch,
		NumHistoricalEpochs:         cliFlags.numHistoricalEpochs,
		ShouldHandleContracts:       cliFlags.shouldHandleContracts,
	})
	if err != nil {
		return err
	}

	networkProvider.LogDescription()

	controllers, err := factory.CreateControllers(networkProvider)
	if err != nil {
		return err
	}

	if cliFlags.shouldEnablePprofEndpoints {
		controllers = append(controllers, newPprofController())
	}

	httpServer, err := createHttpServer(cliFlags.port, controllers...)
	if err != nil {
		return err
	}

	go func() {
		log.Info("Starting HTTP server...", "address", httpServer.Addr)
		err := httpServer.ListenAndServe()
		if err == http.ErrServerClosed {
			log.Info("HTTP server stopped")
		} else {
			log.Error("Unexpected HTTP server error", "err", err)
		}
	}()

	// Set up signal capturing
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, os.Kill)
	<-stop

	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownContext)
	_ = httpServer.Close()
	_ = fileLogging.Close()

	return nil
}

// ISSUE-038: cap on Rosetta request body. Construction / network
// payloads are JSON, typically a few KiB. 4 MiB is well above any
// legitimate Rosetta request and prevents an attacker from streaming a
// multi-GiB body into the SDK's json.Decode/Unmarshal paths
// (server/services/constructionService.go and friends do unbounded
// json.Unmarshal — see ISSUE-038 / ISSUE-031 caller chain).
const maxRosettaRequestBodyBytes int64 = 4 * 1024 * 1024

// limitRosettaRequestBody wraps the Rosetta handler chain with an early
// ContentLength check + MaxBytesReader so the SDK's downstream
// json.Decode never sees more than maxRosettaRequestBodyBytes from any
// single request. Sits in front of CORS so a malicious oversized body
// is rejected before it pays for CORS preflight handling.
func limitRosettaRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRosettaRequestBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRosettaRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func createHttpServer(port int, routers ...server.Router) (*http.Server, error) {
	router := server.NewRouter(
		routers...,
	)

	corsRouter := server.CorsMiddleware(router)
	limitedRouter := limitRosettaRequestBody(corsRouter)

	// ISSUE-039: previously this http.Server had NO timeouts at all,
	// leaving Rosetta exposed to slow-loris and slow-write resource
	// exhaustion. Values match the other services in this stack (chain-go
	// gin webServer / notifier / es-indexer): WriteTimeout is the most
	// generous (60s) to accommodate large Rosetta block/construction
	// responses; the others bound slow-read vectors.
	//
	// ISSUE-038: limitedRouter wraps corsRouter with a body-size cap
	// applied BEFORE the SDK route handlers reach json.Decode.
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           limitedRouter,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return httpServer, nil
}
