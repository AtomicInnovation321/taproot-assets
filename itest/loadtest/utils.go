package loadtest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"testing"

	"github.com/btcsuite/btcd/rpcclient"
	"github.com/lightninglabs/taproot-assets/itest"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/taprpc/assetwalletrpc"
	"github.com/lightninglabs/taproot-assets/taprpc/mintrpc"
	"github.com/lightninglabs/taproot-assets/taprpc/universerpc"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

var (
	// maxMsgRecvSize is the largest message our client will receive. We
	// set this to 200MiB atm.
	maxMsgRecvSize = grpc.MaxCallRecvMsgSize(lnrpc.MaxGrpcMsgSize)
)

type rpcClient struct {
	cfg *TapConfig
	taprpc.TaprootAssetsClient
	universerpc.UniverseClient
	mintrpc.MintClient
	assetwalletrpc.AssetWalletClient
}

func initClients(t *testing.T, ctx context.Context,
	cfg *Config) (*rpcClient, *rpcClient, *rpcclient.Client) {

	// Create tapd clients.
	alice := getTapClient(t, ctx, cfg.Alice.Tapd)

	_, err := alice.GetInfo(ctx, &taprpc.GetInfoRequest{})
	require.NoError(t, err)

	bob := getTapClient(t, ctx, cfg.Bob.Tapd)

	_, err = bob.GetInfo(ctx, &taprpc.GetInfoRequest{})
	require.NoError(t, err)

	// Create bitcoin client.
	bitcoinClient := getBitcoinConn(t, cfg.Bitcoin)

	// Test bitcoin client connection by mining a block.
	itest.MineBlocks(t, bitcoinClient, 1, 0)

	// If we fail from this point onward, we might have created a
	// transaction that isn't mined yet. To make sure we can run the test
	// again, we'll make sure to clean up the mempool by mining a block.
	t.Cleanup(func() {
		itest.MineBlocks(t, bitcoinClient, 1, 0)
	})

	return alice, bob, bitcoinClient
}

func getTapClient(t *testing.T, ctx context.Context,
	cfg *TapConfig) *rpcClient {

	creds := credentials.NewTLS(&tls.Config{})
	if cfg.TLSPath != "" {
		// Load the certificate file now, if specified.
		tlsCert, err := os.ReadFile(cfg.TLSPath)
		require.NoError(t, err)

		cp := x509.NewCertPool()
		ok := cp.AppendCertsFromPEM(tlsCert)
		require.True(t, ok)

		creds = credentials.NewClientTLSFromCert(cp, "")
	}

	// Create a dial options array.
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(maxMsgRecvSize),
	}

	if cfg.MacPath != "" {
		var macBytes []byte
		macBytes, err := os.ReadFile(cfg.MacPath)
		require.NoError(t, err)

		mac := &macaroon.Macaroon{}
		err = mac.UnmarshalBinary(macBytes)
		require.NoError(t, err)

		macCred, err := macaroons.NewMacaroonCredential(mac)
		require.NoError(t, err)

		opts = append(opts, grpc.WithPerRPCCredentials(macCred))
	}

	svrAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conn, err := grpc.DialContext(ctx, svrAddr, opts...)
	require.NoError(t, err)

	assetsClient := taprpc.NewTaprootAssetsClient(conn)
	universeClient := universerpc.NewUniverseClient(conn)
	mintMintClient := mintrpc.NewMintClient(conn)
	assetWalletClient := assetwalletrpc.NewAssetWalletClient(conn)

	client := &rpcClient{
		cfg:                 cfg,
		TaprootAssetsClient: assetsClient,
		UniverseClient:      universeClient,
		MintClient:          mintMintClient,
		AssetWalletClient:   assetWalletClient,
	}

	t.Cleanup(func() {
		err := conn.Close()
		require.NoError(t, err)
	})

	return client
}

func getBitcoinConn(t *testing.T, cfg *BitcoinConfig) *rpcclient.Client {
	var (
		rpcCert []byte
		err     error
	)

	disableTLS := cfg.TLSPath == ""

	// In case we use TLS and a certificate argument is provided, we need to
	// read that file and provide it to the RPC connection as byte slice.
	if !disableTLS {
		rpcCert, err = os.ReadFile(cfg.TLSPath)
		require.NoError(t, err)
	}

	// Connect to the backend with the certs we just loaded.
	connCfg := &rpcclient.ConnConfig{
		Host:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		User:         cfg.User,
		Pass:         cfg.Password,
		HTTPPostMode: true,
		DisableTLS:   disableTLS,
		Certificates: rpcCert,
	}

	client, err := rpcclient.New(connCfg, nil)
	require.NoError(t, err)

	return client
}
