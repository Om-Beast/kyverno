package compiler

import (
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/stretchr/testify/require"
)

func TestVariablesProviderRegisterFieldDeduplicatesNames(t *testing.T) {
	env, err := NewBaseEnv()
	require.NoError(t, err)

	provider := NewVariablesProvider(env.CELTypeProvider())
	provider.RegisterField("count", cel.IntType)
	provider.RegisterField("count", cel.StringType)

	names, found := provider.FindStructFieldNames(VariablesType.DeclaredTypeName())
	require.True(t, found)
	require.Equal(t, []string{"count"}, names)

	fieldType, found := provider.FindStructFieldType(VariablesType.DeclaredTypeName(), "count")
	require.True(t, found)
	require.True(t, fieldType.Type.IsExactType(cel.StringType))
}

func TestVariablesProviderRegisterFieldSameTypeIdempotent(t *testing.T) {
	env, err := NewBaseEnv()
	require.NoError(t, err)

	provider := NewVariablesProvider(env.CELTypeProvider())
	provider.RegisterField("enabled", cel.BoolType)
	provider.RegisterField("enabled", cel.BoolType)

	names, found := provider.FindStructFieldNames(VariablesType.DeclaredTypeName())
	require.True(t, found)
	require.Equal(t, []string{"enabled"}, names)
}

func TestVariablesProviderRegisterFieldDistinctNamesPreserveOrder(t *testing.T) {
	env, err := NewBaseEnv()
	require.NoError(t, err)

	provider := NewVariablesProvider(env.CELTypeProvider())
	provider.RegisterField("alpha", cel.StringType)
	provider.RegisterField("beta", cel.IntType)
	provider.RegisterField("alpha", cel.BoolType)
	provider.RegisterField("gamma", cel.BoolType)
	provider.RegisterField("beta", cel.StringType)

	names, found := provider.FindStructFieldNames(VariablesType.DeclaredTypeName())
	require.True(t, found)
	require.Equal(t, []string{"alpha", "beta", "gamma"}, names)

	for _, tc := range []struct {
		name string
		typ  *types.Type
	}{
		{name: "alpha", typ: cel.BoolType},
		{name: "beta", typ: cel.StringType},
		{name: "gamma", typ: cel.BoolType},
	} {
		fieldType, found := provider.FindStructFieldType(VariablesType.DeclaredTypeName(), tc.name)
		require.True(t, found, "field %q", tc.name)
		require.True(t, fieldType.Type.IsExactType(tc.typ), "field %q", tc.name)
	}
}
