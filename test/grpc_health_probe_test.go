package test

import (
	"github.com/grpc-ecosystem/grpc-health-probe/test/healthserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os/exec"
	"testing"
	"time"
)

func TestHealthProbe(t *testing.T) {
	// Start the GRPC server
	go healthserver.Start()

	// Wait till the grpc server is up
	time.Sleep(100 * time.Millisecond)

	// Now execute the health probe
	tmpProcess := exec.Command(
		"grpc-health-probe",
		"-addr",
		healthserver.GRPC_TEST_ADDRESS,
	)
	output, err := tmpProcess.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(output), "SERVING")

	// Then stop the health service
	healthserver.Stop()
}
