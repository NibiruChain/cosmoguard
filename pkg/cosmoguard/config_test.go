package cosmoguard

import (
	"testing"

	"gotest.tools/assert"
)

func TestReadConfigFromFile(t *testing.T) {
	_, err := ReadConfigFromFile("../../example.config.yml")
	assert.NilError(t, err)
}
