package factory

import (
	"testing"

	"github.com/multiversx/mx-chain-go/sharding/nodesCoordinator"
	"github.com/stretchr/testify/require"
)

func TestCreateNetworkProviderShouldRejectInvalidShardConfig(t *testing.T) {
	provider, err := CreateNetworkProvider(ArgsCreateNetworkProvider{
		NumShards:              0,
		ObservedActualShard:    0,
		NativeCurrencySymbol:   "EGLD",
		ObserverUrl:            "http://observer",
		GenesisBlockHash:       "hash",
		GasPriceModifier:       0.01,
		MinGasPrice:            1000000000,
		MinGasLimit:            50000,
		GasLimitCustomTransfer: 200000,
	})

	require.Nil(t, provider)
	require.ErrorIs(t, err, nodesCoordinator.ErrInvalidNumberOfShards)
}
