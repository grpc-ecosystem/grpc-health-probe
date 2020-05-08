package test

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os/exec"
	"testing"
	"time"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestHealthProbeAsServing(t *testing.T) {
	// Start the GRPC server
 	_, stopFnc := makeServer(t, healthpb.HealthCheckResponse_SERVING)

	// Wait till the grpc server is up
	time.Sleep(100 * time.Millisecond)

	// Now execute the health probe, it should not fail as status is serving
	testStatus(t, "SERVING", false)

	// Then stop the health service
	stopFnc()
}

func TestHealthProbeWhenNotServing(t *testing.T) {
	// Start the GRPC server
	_, stopFnc := makeServer(t, healthpb.HealthCheckResponse_NOT_SERVING)

	// Wait till the grpc server is up
	time.Sleep(100 * time.Millisecond)

	// Now execute the health probe, it should not fail as status is serving
	testStatus(t, "NOT SERVING", true)

	// Then stop the health service
	stopFnc()
}

// testStatus test the status of grpc server with expected status
func testStatus(t *testing.T, expectedStatus string, isFail bool) {
	tmpProcess := exec.Command(
		"grpc-health-probe",
		"-addr",
		GRPC_ADDRESS,
	)
	output, err := tmpProcess.CombinedOutput()
	if !isFail {
		require.NoError(t, err, "")
		assert.Contains(t, string(output), expectedStatus)
	} else {
		require.Error(t, err)
	}

}
