package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateConcurrencyQueueGroupLimitValid(t *testing.T) {
	original := ConcurrencyQueueGroupLimit
	t.Cleanup(func() { ConcurrencyQueueGroupLimit = original })

	err := UpdateConcurrencyQueueGroupLimitByJSONString(`{"default":2,"vip":5}`)
	require.NoError(t, err)
	assert.Equal(t, 2, ConcurrencyQueueGroupLimit["default"])
	assert.Equal(t, 5, ConcurrencyQueueGroupLimit["vip"])
}

func TestUpdateConcurrencyQueueGroupLimitRejectsNegative(t *testing.T) {
	err := UpdateConcurrencyQueueGroupLimitByJSONString(`{"bad":-1}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative")
}

func TestUpdateConcurrencyQueueGroupLimitRejectsInvalidJSON(t *testing.T) {
	err := UpdateConcurrencyQueueGroupLimitByJSONString(`not-json`)
	require.Error(t, err)
}
