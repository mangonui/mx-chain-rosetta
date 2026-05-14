package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountQueryOptionsConstructors(t *testing.T) {
	onFinal := NewAccountQueryOptionsOnFinalBlock()
	require.True(t, onFinal.OnFinalBlock)
	require.False(t, onFinal.BlockNonce.HasValue)
	require.Nil(t, onFinal.BlockHash)

	withNonce := NewAccountQueryOptionsWithBlockNonce(42)
	require.False(t, withNonce.OnFinalBlock)
	require.True(t, withNonce.BlockNonce.HasValue)
	require.Equal(t, uint64(42), withNonce.BlockNonce.Value)
	require.Nil(t, withNonce.BlockHash)

	hash := []byte("hash")
	withHash := NewAccountQueryOptionsWithBlockHash(hash)
	require.False(t, withHash.OnFinalBlock)
	require.False(t, withHash.BlockNonce.HasValue)
	require.Equal(t, hash, withHash.BlockHash)
}
