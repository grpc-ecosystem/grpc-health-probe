package test

import (
	"github.com/grpc-ecosystem/grpc-health-probe/test/healthserver"
	healthpb "github.com/grpc-ecosystem/grpc-health-probe/test/healthserver/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os/exec"
	"testing"
	"time"
)

func TestHealthProbeAsServing(t *testing.T) {
	// Start the GRPC server
	go healthserver.Start()

	// Wait till the grpc server is up
	time.Sleep(100 * time.Millisecond)

	// Now execute the health probe, it should not fail as status is serving
	testStatus(t, "SERVING", false)

	// This should fail as the status is set to no serving.
	healthserver.SetStatus(healthpb.HealthCheckResponse_NOT_SERVING)
	testStatus(t, "NOT SERVING", true)

	// Then stop the health service
	healthserver.Stop()
}

// testStatus test the status of grpc server with expected status
func testStatus(t *testing.T, expectedStatus string, isFail bool) {
	tmpProcess := exec.Command(
		"grpc-health-probe",
		"-addr",
		healthserver.GRPC_TEST_ADDRESS,
	)
	output, err := tmpProcess.CombinedOutput()
	if !isFail {
		require.NoError(t, err, "")
		assert.Contains(t, string(output), expectedStatus)
	} else {
		require.Error(t, err)
	}

}
